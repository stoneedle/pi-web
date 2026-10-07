//go:build title_e2e

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"pi-web/internal/sessions"
)

// The actor is the title fork's real owner module backed by native Pi session
// persistence. Its model result is held at the model seam until after rename.
func TestTitleOwnerRenameLateSummaryAndReopen(t *testing.T) {
	pluginRoot := os.Getenv("PI_TITLE_GLYPHS_ROOT")
	if pluginRoot == "" {
		t.Fatal("PI_TITLE_GLYPHS_ROOT must point to the pi-title-glyphs checkout (with npm dependencies installed)")
	}
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "node", filepath.Join(pluginRoot, "test", "owner-fixture.mjs"), root)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = io.WriteString(stdin, "finish\nshutdown\n")
		_ = stdin.Close()
		if err := command.Wait(); err != nil {
			t.Errorf("title actor exited: %v\n%s", err, stderr.String())
		}
	}()
	decoder := json.NewDecoder(stdout)
	var state struct {
		Phase       string `json:"phase"`
		SessionFile string `json:"sessionFile"`
		Name        string `json:"name"`
	}
	readState := func(phase, name string) {
		t.Helper()
		if err := decoder.Decode(&state); err != nil {
			t.Fatalf("read %s: %v\n%s", phase, err, stderr.String())
		}
		if state.Phase != phase || state.Name != name {
			t.Fatalf("state = %#v, want %s / %s", state, phase, name)
		}
	}
	readState("ready", "定位字幕导入错位")
	s := &Server{agentDir: root, sessionsDir: filepath.Dir(filepath.Dir(state.SessionFile)), cache: sessions.NewCache(), now: time.Now}
	api := httptest.NewServer(http.HandlerFunc(s.handleRenameSession))
	defer api.Close()
	rename := func(name string) {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"name": name})
		response, err := http.Post(api.URL+"/api/rename-session?id="+filepath.Base(state.SessionFile), "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var payload struct {
			Name  string `json:"name"`
			Error string `json:"error"`
		}
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK || payload.Name != name {
			t.Fatalf("rename status=%d response=%#v", response.StatusCode, payload)
		}
	}
	manual := "网页手动命名 👨‍👩‍👧‍👦"
	rename(manual)
	if _, err := io.WriteString(stdin, "finish\n"); err != nil {
		t.Fatal(err)
	}
	readState("finished", manual)
	if _, err := io.WriteString(stdin, "stop\n"); err != nil {
		t.Fatal(err)
	}
	readState("stopped", manual)
	registration := filepath.Join(root, "extension-data", "pi-title-glyphs", "owners", filepath.Base(state.SessionFile)+".json")
	if _, err := os.Stat(registration); !os.IsNotExist(err) {
		t.Fatalf("owner registration survived shutdown: %v", err)
	}
	offline := "离线改名后重开 🇨🇳"
	rename(offline)
	if _, err := io.WriteString(stdin, "reopen\n"); err != nil {
		t.Fatal(err)
	}
	readState("reopened", offline)
	resolved, err := sessions.ResolveByID(s.sessionsDir, filepath.Base(state.SessionFile))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Session.Name != offline {
		t.Fatalf("web sees %q, Pi sees %q", resolved.Session.Name, offline)
	}
}
