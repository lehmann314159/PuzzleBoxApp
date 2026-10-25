package main

import (
	"crypto/rand"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"puzzleboxapp/internal/agent"
	"puzzleboxapp/internal/api"
	"puzzleboxapp/internal/box"
	"puzzleboxapp/internal/mcp"
	"puzzleboxapp/internal/store"
	"puzzleboxapp/web"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	dbPath := env("BOX_DB", "data/box.db")
	if dir := filepath.Dir(dbPath); dir != "." {
		os.MkdirAll(dir, 0755)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatal("Failed to open database:", err)
	}
	defer st.Close()

	a := &api.API{
		Box:      &box.Service{Store: st, ValidateArgs: env("VALIDATE_ARGS", "1") == "1"},
		Store:    st,
		Instance: env("INSTANCE", "go1"),
		UIToken:  os.Getenv("UI_TOKEN"),
	}
	secret := []byte(os.Getenv("BOX_SECRET"))
	if len(secret) == 0 { // fine for one instance; several behind a balancer must share BOX_SECRET
		secret = make([]byte, 32)
		rand.Read(secret)
	}
	m := &mcp.Server{
		Box: a.Box, Store: st, Instance: a.Instance, Secret: secret, Bearer: os.Getenv("MCP_BEARER"),
		StrictParamHeaders: env("STRICT_PARAM_HEADERS", "1") == "1",
	}
	for _, o := range strings.Split(os.Getenv("ALLOW_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			m.AllowOrigins = append(m.AllowOrigins, o)
		}
	}
	mux := http.NewServeMux()
	a.Routes(mux)
	m.Routes(mux)

	port := env("PORT", "9100")
	temp, _ := strconv.ParseFloat(env("AGENT_TEMPERATURE", "0.1"), 64)
	maxSteps, _ := strconv.Atoi(env("MAX_STEPS", "60"))
	callTimeout, _ := time.ParseDuration(env("OLLAMA_CALL_TIMEOUT", "60s"))
	var models []string
	for _, name := range strings.Split(os.Getenv("OLLAMA_MODELS"), ",") {
		if name = strings.TrimSpace(name); name != "" {
			models = append(models, name)
		}
	}
	agent.New(agent.Config{
		SelfURL: "http://127.0.0.1:" + port + "/mcp", Bearer: m.Bearer,
		OllamaURL: env("OLLAMA_URL", "http://127.0.0.1:11434"), Models: models, DefaultModel: os.Getenv("OLLAMA_MODEL"),
		MaxSteps: maxSteps, CallTimeout: callTimeout, Temperature: temp, Think: agent.ParseThink(os.Getenv("OLLAMA_THINK")), Pause: 400 * time.Millisecond, MaxRunning: 2,
	}).Routes(mux)
	page := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(web.Index)
	}
	mux.HandleFunc("GET /{$}", page)
	mux.HandleFunc("GET /ui", page)

	addr := env("HOST", "127.0.0.1") + ":" + port
	log.Printf("puzzlebox listening on http://%s", addr)
	log.Fatal(http.ListenAndServe(addr, a.Guard(mux)))
}
