package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/process"
	"pi-web/internal/sessions"
)

// Active-session names are committed by the Pi instance holding the session.
// An unavailable live owner is an error; only inactive sessions are edited here.
func (s *Server) renameSession(ctx context.Context, resolved sessions.ResolvedSession, name string) (string, error) {
	var owner struct {
		PID       int    `json:"pid"`
		Port      int    `json:"port"`
		Token     string `json:"token"`
		SessionID string `json:"sessionId"`
	}
	registration := filepath.Join(s.agentDir, "extension-data", "pi-title-glyphs", "owners", filepath.Base(resolved.Path)+".json")
	data, err := os.ReadFile(registration)
	active := false
	if err == nil {
		if err := json.Unmarshal(data, &owner); err != nil {
			return "", fmt.Errorf("read title owner: %w", err)
		}
		if owner.PID <= 0 || owner.PID > math.MaxInt32 || owner.Port < 1 || owner.Port > 65535 || owner.Token == "" || owner.SessionID == "" {
			return "", fmt.Errorf("invalid title owner registration")
		}
		active, err = process.PidExistsWithContext(ctx, int32(owner.PID))
		if err != nil {
			return "", fmt.Errorf("check title owner: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read title owner: %w", err)
	}

	if !active {
		if sender, ok := s.chatSender.(workerSnapshotter); ok {
			for _, worker := range sender.Snapshot() {
				if worker.SessionID == resolved.Session.ID {
					return "", fmt.Errorf("active session has no title owner; load pi-title-glyphs in its Pi instance")
				}
			}
		}
		if err := sessions.RenameSession(resolved.Path, name, s.now); err != nil {
			return "", err
		}
		return name, nil
	}
	if owner.SessionID != resolved.Session.SessionUUID {
		return "", fmt.Errorf("title owner belongs to a different session")
	}
	payload, err := json.Marshal(struct {
		Operation string `json:"operation"`
		Value     string `json:"value"`
		SessionID string `json:"sessionId"`
	}{"rename", name, owner.SessionID})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/title", owner.Port), bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+owner.Token)
	req.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("rename through title owner: %w", err)
	}
	defer response.Body.Close()
	var reply struct {
		Name  string `json:"name"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&reply); err != nil {
		return "", fmt.Errorf("read title owner response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("title owner rejected rename: %s", reply.Error)
	}
	if strings.TrimSpace(reply.Name) == "" {
		return "", fmt.Errorf("title owner returned an empty name")
	}
	return reply.Name, nil
}
