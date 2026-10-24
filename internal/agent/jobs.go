package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// Step is one line of a transcript: what a model said, what was called, what the box answered.
type Step struct {
	N       int    `json:"n"`
	Kind    string `json:"kind"` // note | model | tool | ask | final | error
	Text    string `json:"text,omitempty"`
	Name    string `json:"name,omitempty"`
	Args    any    `json:"args,omitempty"`
	IsError bool   `json:"is_error,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
	BoxID   string `json:"box_id,omitempty"` // the box the chat is working on, so the page can pass it back
}

var (
	ErrBusy       = errors.New("too many jobs are running; try again in a moment")
	ErrNotWaiting = errors.New("that job is not waiting for an answer")
	errDeclined   = errors.New("declined")
)

const askTimeout = 5 * time.Minute

// Job is a running or finished piece of work whose steps can be polled.
type Job struct {
	ID string

	mu      sync.Mutex
	steps   []Step
	done    bool
	waiting bool
	answer  chan bool
	cancel  context.CancelFunc
	ended   time.Time
}

func (j *Job) emit(s Step) {
	j.mu.Lock()
	defer j.mu.Unlock()
	s.N = len(j.steps)
	j.steps = append(j.steps, s)
}

// Since returns the steps after index n and whether the job has finished.
func (j *Job) Since(n int) ([]Step, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if n < 0 || n > len(j.steps) {
		n = len(j.steps)
	}
	return append([]Step{}, j.steps[n:]...), j.done
}

// Answer delivers a human's yes or no to a paused job.
func (j *Job) Answer(approve bool) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.waiting {
		return ErrNotWaiting
	}
	j.waiting = false
	j.answer <- approve
	return nil
}

// Stop cancels the job.
func (j *Job) Stop() { j.cancel() }

// ask pauses the job until a human answers, the job is stopped, or time runs out.
func (j *Job) ask(ctx context.Context, prompt string) (bool, error) {
	j.mu.Lock()
	j.waiting = true
	j.mu.Unlock()
	j.emit(Step{Kind: "ask", Prompt: prompt})
	defer func() { j.mu.Lock(); j.waiting = false; j.mu.Unlock() }()
	select {
	case a := <-j.answer:
		return a, nil
	case <-ctx.Done():
		return false, ctx.Err()
	case <-time.After(askTimeout):
		return false, errors.New("nobody answered the box's question")
	}
}

// Jobs keeps the jobs for a while after they finish so the page can read their last steps.
type Jobs struct {
	MaxRunning int

	mu   sync.Mutex
	jobs map[string]*Job
}

// Start runs fn as a job. fn's error, if any, becomes an error step.
func (js *Jobs) Start(fn func(ctx context.Context, j *Job) error) (*Job, error) {
	js.mu.Lock()
	defer js.mu.Unlock()
	if js.jobs == nil {
		js.jobs = map[string]*Job{}
	}
	running := 0
	for id, j := range js.jobs {
		j.mu.Lock()
		switch {
		case !j.done:
			running++
		case time.Since(j.ended) > 30*time.Minute:
			delete(js.jobs, id)
		}
		j.mu.Unlock()
	}
	if running >= max(js.MaxRunning, 1) {
		return nil, ErrBusy
	}
	b := make([]byte, 8)
	rand.Read(b)
	ctx, cancel := context.WithCancel(context.Background())
	j := &Job{ID: hex.EncodeToString(b), answer: make(chan bool, 1), cancel: cancel}
	js.jobs[j.ID] = j
	go func() {
		defer cancel()
		if err := fn(ctx, j); err != nil {
			msg := err.Error()
			if errors.Is(err, context.Canceled) {
				msg = "stopped"
			}
			j.emit(Step{Kind: "error", Text: msg, IsError: true})
		}
		j.mu.Lock()
		j.done, j.ended = true, time.Now()
		j.mu.Unlock()
	}()
	return j, nil
}

// Get finds a job by id.
func (js *Jobs) Get(id string) *Job {
	js.mu.Lock()
	defer js.mu.Unlock()
	return js.jobs[id]
}
