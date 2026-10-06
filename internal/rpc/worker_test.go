package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"pi-web/internal/workers"
)

type nopWriteCloser struct{ w io.Writer }

func (n nopWriteCloser) Write(p []byte) (int, error) { return n.w.Write(p) }
func (n nopWriteCloser) Close() error                { return nil }

func waitForPending(t *testing.T, w *piRPCWorker, id string) {
	t.Helper()
	for i := 0; i < 1000; i++ {
		w.mu.Lock()
		_, ok := w.pending[id]
		w.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("pending request %q never registered", id)
}

func TestWorkerDirUsesSessionCWD(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/sess.jsonl"
	if err := os.WriteFile(path, []byte(`{"type":"session","cwd":`+strconv.Quote(dir)+`}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := workerDir(path); got != dir {
		t.Fatalf("workerDir = %q, want session cwd %q", got, dir)
	}
}

func TestWorkerDirFallsBackToTempWhenCWDMissing(t *testing.T) {
	path := t.TempDir() + "/sess.jsonl"
	if err := os.WriteFile(path, []byte(`{"type":"session","cwd":""}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := workerDir(path); got != detachedPiDir() {
		t.Fatalf("workerDir = %q, want detached dir %q", got, detachedPiDir())
	}
}

func TestWorkerStateFollowsRunLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events []string
	}{
		{
			name: "normal run",
			events: []string{
				`{"type":"agent_start"}`,
				`{"type":"message_end"}`,
				`{"type":"turn_end"}`,
				`{"type":"agent_end","willRetry":false}`,
			},
		},
		{
			name: "automatic retry",
			events: []string{
				`{"type":"agent_start"}`,
				`{"type":"agent_end","willRetry":true}`,
				`{"type":"auto_retry_start","delayMs":2000}`,
				`{"type":"agent_start"}`,
				`{"type":"auto_retry_end","success":true}`,
				`{"type":"agent_end","willRetry":false}`,
			},
		},
		{
			name: "compaction recovery",
			events: []string{
				`{"type":"agent_start"}`,
				`{"type":"agent_end","willRetry":false}`,
				`{"type":"compaction_start"}`,
				`{"type":"compaction_end","willRetry":true}`,
				`{"type":"agent_start"}`,
				`{"type":"agent_end"}`,
			},
		},
		{
			name: "queued continuation",
			events: []string{
				`{"type":"agent_start"}`,
				`{"type":"agent_end","willRetry":false}`,
				`{"type":"agent_start"}`,
				`{"type":"agent_end"}`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &piRPCWorker{status: workers.WorkerStatus{State: workers.WorkerStateIdle}}
			for _, event := range tc.events {
				w.handleRPCLine(event)
				if got := w.Status().State; got != workers.WorkerStateRunning {
					t.Fatalf("after %s: status = %q, want running until settlement", event, got)
				}
			}
			w.handleRPCLine(`{"type":"agent_settled"}`)
			if got := w.Status().State; got != workers.WorkerStateIdle {
				t.Fatalf("settled status = %q, want idle", got)
			}
		})
	}
}

func TestHandleRPCLineEmitsStreamPreviewCallbacks(t *testing.T) {
	var previews []StreamPreview
	w := &piRPCWorker{
		status:        workers.WorkerStatus{State: workers.WorkerStateIdle},
		pending:       make(map[string]chan response),
		streamSink:    func(preview StreamPreview) { previews = append(previews, preview) },
		streamPreview: &streamPreviewAccumulator{},
	}

	w.handleRPCLine(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"hel"}}`)
	w.handleRPCLine(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"lo"}}`)

	if len(previews) != 2 {
		t.Fatalf("previews = %d, want 2", len(previews))
	}
	if previews[0].Content != "hel" || previews[0].Done {
		t.Fatalf("first preview = %+v", previews[0])
	}
	if previews[1].Content != "hello" || previews[1].Done {
		t.Fatalf("second preview = %+v", previews[1])
	}
}

func TestHandleRPCLineEmitsDonePreviewOnAgentEnd(t *testing.T) {
	var previews []StreamPreview
	w := &piRPCWorker{
		status:        workers.WorkerStatus{State: workers.WorkerStateIdle},
		pending:       make(map[string]chan response),
		streamSink:    func(preview StreamPreview) { previews = append(previews, preview) },
		streamPreview: &streamPreviewAccumulator{},
	}

	w.handleRPCLine(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"hello"}}`)
	w.handleRPCLine(`{"type":"agent_end"}`)

	if len(previews) != 2 {
		t.Fatalf("previews = %d, want 2", len(previews))
	}
	if previews[1].Content != "hello" || !previews[1].Done {
		t.Fatalf("done preview = %+v", previews[1])
	}
}

func TestHandleRPCLineKeepsRunActiveAfterMessageEnd(t *testing.T) {
	w := &piRPCWorker{
		status:  workers.WorkerStatus{State: workers.WorkerStateRunning},
		pending: make(map[string]chan response),
	}

	w.handleRPCLine(`{"type":"message_end"}`)

	if got := w.Status(); got.State != workers.WorkerStateRunning {
		t.Fatalf("status = %q, want running", got.State)
	}
}

func TestGetCommandsReturnsCachedWithoutRPC(t *testing.T) {
	w := &piRPCWorker{
		pending:        make(map[string]chan response),
		commands:       []workers.SlashCommand{{Name: "skill:memory", Source: "skill"}},
		commandsCached: true,
	}
	// stdin is nil: the cache path must not attempt any RPC write.
	got, err := w.GetCommands(context.Background())
	if err != nil {
		t.Fatalf("GetCommands error: %v", err)
	}
	if len(got) != 1 || got[0].Name != "skill:memory" {
		t.Fatalf("got = %#v", got)
	}
}

func TestGetCommandsParsesResponseAndCaches(t *testing.T) {
	var buf bytes.Buffer
	w := &piRPCWorker{
		stdin:   nopWriteCloser{&buf},
		pending: make(map[string]chan response),
	}

	type result struct {
		cmds []workers.SlashCommand
		err  error
	}
	resCh := make(chan result, 1)
	go func() {
		cmds, err := w.GetCommands(context.Background())
		resCh <- result{cmds, err}
	}()

	waitForPending(t, w, "req-1")
	w.handleRPCLine(`{"type":"response","id":"req-1","command":"get_commands","success":true,"data":{"commands":[{"name":"skill:memory","description":"mem","source":"skill"},{"name":"btw","description":"side chat","source":"extension"}]}}`)

	got := <-resCh
	if got.err != nil {
		t.Fatalf("GetCommands error: %v", got.err)
	}
	if len(got.cmds) != 2 {
		t.Fatalf("commands = %#v", got.cmds)
	}
	if got.cmds[0].Name != "skill:memory" || got.cmds[0].Source != "skill" || got.cmds[0].Description != "mem" {
		t.Fatalf("first command = %#v", got.cmds[0])
	}

	// Second call must hit the cache: no further RPC write to stdin.
	buf.Reset()
	cached, err := w.GetCommands(context.Background())
	if err != nil {
		t.Fatalf("cached GetCommands error: %v", err)
	}
	if len(cached) != 2 {
		t.Fatalf("cached commands = %#v", cached)
	}
	if buf.Len() != 0 {
		t.Fatalf("cache hit wrote to stdin: %q", buf.String())
	}
}

func TestHandleRPCLineIgnoresMalformedJSON(t *testing.T) {
	w := &piRPCWorker{
		status:  workers.WorkerStatus{State: workers.WorkerStateIdle},
		pending: make(map[string]chan response),
	}

	w.handleRPCLine(`{not-json}`)

	if got := w.Status(); got.State != workers.WorkerStateIdle {
		t.Fatalf("status = %q, want idle", got.State)
	}
}

func TestHandleRPCLineTracksThinkingAndTextStreamEvents(t *testing.T) {
	w := &piRPCWorker{
		status:  workers.WorkerStatus{State: workers.WorkerStateRunning},
		pending: make(map[string]chan response),
	}

	for _, line := range []string{
		`{"type":"message_update","assistantMessageEvent":{"type":"thinking_end"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_start"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_end","content":"done"}}`,
	} {
		w.handleRPCLine(line)
		if got := w.Status(); got.State != workers.WorkerStateRunning {
			t.Fatalf("line %s => status = %q, want running", strings.TrimSpace(line), got.State)
		}
	}
}

type callbackWriter func([]byte) (int, error)

func (w callbackWriter) Write(p []byte) (int, error) { return w(p) }

func TestCommandAcknowledgementsPreserveRunState(t *testing.T) {
	for _, command := range []string{"abort", "set_model"} {
		for _, state := range []workers.State{workers.WorkerStateIdle, workers.WorkerStateRunning, workers.WorkerStateError} {
			t.Run(command+"/"+string(state), func(t *testing.T) {
				w := &piRPCWorker{
					status:  workers.WorkerStatus{State: state},
					pending: make(map[string]chan response),
				}
				w.stdin = nopWriteCloser{w: callbackWriter(func(p []byte) (int, error) {
					var request struct{ ID string }
					if err := json.Unmarshal(p, &request); err != nil {
						return 0, err
					}
					line, err := json.Marshal(map[string]any{
						"id": request.ID, "type": "response", "success": true,
						"data": map[string]any{
							"id": "test-model", "name": "Test Model", "provider": "test-provider",
							"model":         map[string]any{"id": "test-model", "name": "Test Model", "provider": "test-provider"},
							"thinkingLevel": "high",
						},
					})
					if err != nil {
						return 0, err
					}
					w.handleRPCLine(string(line))
					return len(p), nil
				})}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				var err error
				if command == "abort" {
					err = w.Abort(ctx)
				} else {
					err = w.SetModel(ctx, "test-provider", "test-model")
				}
				if err != nil {
					t.Fatal(err)
				}
				if got := w.Status(); got.State != state {
					t.Fatalf("%s acknowledgement changed status from %q to %q", command, state, got.State)
				}
				if command == "set_model" {
					if got := w.Status(); got.Model != "test-model" || got.ModelName != "Test Model" || got.ModelProvider != "test-provider" {
						t.Fatalf("set_model did not refresh model metadata: %+v", got)
					}
				}
			})
		}
	}
}

func TestLifecycleEventsDoNotResetWorkerError(t *testing.T) {
	for _, event := range []string{
		`{"type":"agent_start"}`,
		`{"type":"agent_end"}`,
		`{"type":"agent_settled"}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"late output"}}`,
	} {
		t.Run(event, func(t *testing.T) {
			const cause = "pi process exited"
			w := &piRPCWorker{status: workers.WorkerStatus{State: workers.WorkerStateError, Error: cause}}
			w.handleRPCLine(event)
			if got := w.Status(); got.State != workers.WorkerStateError || got.Error != cause {
				t.Fatalf("late event reset process error: %+v", got)
			}
		})
	}
}

type closeTrackingWriter struct {
	closed chan struct{}
	once   sync.Once
}

func (w *closeTrackingWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *closeTrackingWriter) Close() error {
	w.once.Do(func() { close(w.closed) })
	return nil
}

func TestRecoveringWorkerSurvivesIdleReaper(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events []string
		quiet  time.Duration
	}{
		{
			name:  "retry backoff",
			quiet: 1200 * time.Millisecond,
		},
		{
			name:   "retried request awaiting output",
			events: []string{`{"type":"agent_start"}`},
			quiet:  1200 * time.Millisecond,
		},
		{
			name: "silent tool after retry",
			events: []string{
				`{"type":"agent_start"}`,
				`{"type":"message_end"}`,
				`{"type":"tool_execution_start"}`,
			},
			quiet: 3200 * time.Millisecond,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stdin := &closeTrackingWriter{closed: make(chan struct{})}
			w := &piRPCWorker{stdin: stdin, status: workers.WorkerStatus{State: workers.WorkerStateRunning}}
			w.lastActive.Store(time.Now().Add(-time.Hour).UnixNano())
			manager := workers.NewManagerWithTTL(func(string, string) (workers.ChatWorker, error) {
				return w, nil
			}, time.Millisecond)
			t.Cleanup(func() { _ = manager.Close() })
			if err := manager.EnsureWorker(context.Background(), "recovering", "unused"); err != nil {
				t.Fatal(err)
			}
			w.handleRPCLine(`{"type":"agent_end","willRetry":true}`)
			w.handleRPCLine(`{"type":"auto_retry_start","delayMs":2000,"errorMessage":"WebSocket closed 1012"}`)
			for _, event := range tc.events {
				w.handleRPCLine(event)
			}

			select {
			case <-stdin.closed:
				t.Fatal("reaper closed a recovering Pi process")
			case <-time.After(tc.quiet):
			}
			if got := manager.Status("recovering").State; got != workers.WorkerStateRunning {
				t.Fatalf("quiet recovery status = %q, want running", got)
			}

			w.handleRPCLine(`{"type":"agent_settled"}`)
			select {
			case <-stdin.closed:
			case <-time.After(2 * time.Second):
				t.Fatal("settled idle worker was not reaped")
			}
		})
	}
}
