package mcp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// state is what a paused request carries: which box, target and verb it was for, and when it expires.
type state struct {
	Box    string  `json:"b"`
	Target string  `json:"t"`
	Verb   string  `json:"v"`
	Exp    float64 `json:"exp"`
}

func (s *Server) sign(raw []byte) []byte {
	m := hmac.New(sha256.New, s.Secret)
	m.Write(raw)
	return m.Sum(nil)
}

// seal makes an opaque, tamper-proof token (HMAC-SHA256, same format as the Python demo).
func (s *Server) seal(p state) string {
	raw, _ := json.Marshal(p)
	return base64.URLEncoding.EncodeToString(raw) + "." + base64.URLEncoding.EncodeToString(s.sign(raw))
}

// unseal returns nil for anything forged, malformed or expired.
func (s *Server) unseal(token string) *state {
	a, b, ok := strings.Cut(token, ".")
	if !ok {
		return nil
	}
	raw, err1 := base64.URLEncoding.DecodeString(a)
	sig, err2 := base64.URLEncoding.DecodeString(b)
	if err1 != nil || err2 != nil || !hmac.Equal(sig, s.sign(raw)) {
		return nil
	}
	var p state
	if json.Unmarshal(raw, &p) != nil || p.Exp <= float64(time.Now().UnixNano())/1e9 {
		return nil
	}
	return &p
}
