package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/c0dev0id/claude-live-coding/internal/event"
)

type rawEntry struct {
	Type    string      `json:"type"`
	IsMeta  bool        `json:"isMeta"`
	Message *rawMessage `json:"message"`
	CWD     string      `json:"cwd"`
}

type rawMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string or []contentBlock
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Name      string          `json:"name"`
	ID        string          `json:"id"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
}

// parser holds state across JSONL lines within a single session.
type parser struct {
	cwd          string
	pendingEdits map[string]string // tool_use_id → file path
	genDiffs     bool
}

func (p *parser) parse(line []byte) []event.Event {
	var entry rawEntry
	if err := json.Unmarshal(line, &entry); err != nil || entry.Message == nil {
		return nil
	}
	if entry.CWD != "" {
		p.cwd = entry.CWD
	}

	now := time.Now().UnixMilli()
	msg := entry.Message

	switch entry.Type {
	case "user":
		if entry.IsMeta {
			return nil
		}
		return p.parseUserContent(msg.Content, now)
	case "assistant":
		return p.parseAssistantContent(msg.Content, now)
	}
	return nil
}

func (p *parser) parseUserContent(raw json.RawMessage, ts int64) []event.Event {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if strings.HasPrefix(s, "<") {
			return nil
		}
		return []event.Event{{Kind: event.KindUser, Text: s, Ts: ts}}
	}

	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil
	}

	var events []event.Event
	for _, b := range blocks {
		if b.Type == "text" && !strings.HasPrefix(b.Text, "<") {
			events = append(events, event.Event{Kind: event.KindUser, Text: b.Text, Ts: ts})
		}
		if b.Type == "tool_result" && p.genDiffs {
			if file, ok := p.pendingEdits[b.ToolUseID]; ok {
				delete(p.pendingEdits, b.ToolUseID)
				if diff := gitDiff(p.cwd, file); diff != "" {
					events = append(events, event.Event{
						Kind: event.KindDiff,
						File: file,
						Text: diff,
						Ts:   ts,
					})
				}
			}
		}
	}
	return events
}

func (p *parser) parseAssistantContent(raw json.RawMessage, ts int64) []event.Event {
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil
	}
	var events []event.Event
	for _, b := range blocks {
		switch b.Type {
		case "text":
			events = append(events, event.Event{Kind: event.KindAssistant, Text: b.Text, Ts: ts})
		case "tool_use":
			events = append(events, event.Event{
				Kind:     event.KindTool,
				ToolName: b.Name,
				Text:     summarizeInput(b.Name, b.Input),
				Ts:       ts,
			})
			if b.Name == "Write" || b.Name == "Edit" {
				if file := extractFilePath(b.Input); file != "" {
					p.pendingEdits[b.ID] = file
				}
			}
		}
	}
	return events
}

func extractFilePath(raw json.RawMessage) string {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	var s string
	if v, ok := m["file_path"]; ok {
		json.Unmarshal(v, &s)
	}
	return s
}

func summarizeInput(toolName string, raw json.RawMessage) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return toolName
	}
	for _, key := range []string{"description", "command", "file_path", "pattern", "prompt", "query", "path"} {
		if v, ok := m[key]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil && s != "" {
				return s
			}
		}
	}
	return toolName
}

func gitDiff(cwd, file string) string {
	if cwd == "" || file == "" {
		return ""
	}
	// make path relative so git diff works regardless of how file_path was stored
	rel := file
	if filepath.IsAbs(file) {
		var err error
		rel, err = filepath.Rel(cwd, file)
		if err != nil {
			return ""
		}
	}
	cmd := exec.Command("git", "diff", "HEAD", "--", rel)
	cmd.Dir = cwd
	out, _ := cmd.Output()
	return string(out)
}

func postEvent(server string, e event.Event) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	resp, err := http.Post(server+"/ingest", "application/json", bytes.NewReader(data))
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
	return nil
}

func postWithRetry(server string, e event.Event) {
	for {
		if err := postEvent(server, e); err != nil {
			log.Printf("post failed: %v — retrying in 2s", err)
			time.Sleep(2 * time.Second)
			continue
		}
		return
	}
}

func latestSessionFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(home, ".claude", "projects")
	var best string
	var bestMod time.Time
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.Contains(path, "/subagents/") {
			return nil
		}
		if filepath.Ext(path) == ".jsonl" && info.ModTime().After(bestMod) {
			best = path
			bestMod = info.ModTime()
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if best == "" {
		return "", fmt.Errorf("no .jsonl session file found under %s", root)
	}
	return best, nil
}

func tailFile(path string, server string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()
	if err := watcher.Add(path); err != nil {
		return err
	}

	p := &parser{pendingEdits: make(map[string]string)}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)

	send := func(line []byte) {
		for _, e := range p.parse(line) {
			postWithRetry(server, e)
		}
	}

	// replay existing content without generating diffs
	for scanner.Scan() {
		send(scanner.Bytes())
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	// switch to live mode: diffs are now generated
	p.genDiffs = true

	reader := bufio.NewReader(f)
	for {
		select {
		case ev, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if ev.Has(fsnotify.Write) || ev.Has(fsnotify.Create) {
				for {
					line, err := reader.ReadBytes('\n')
					if len(line) > 0 {
						send(bytes.TrimRight(line, "\n"))
					}
					if err == io.EOF {
						break
					}
					if err != nil {
						return err
					}
				}
			}
		case err := <-watcher.Errors:
			return err
		}
	}
}

func main() {
	session := flag.String("session", "", "path to Claude Code .jsonl session file (auto-detect if empty)")
	server := flag.String("server", "http://localhost:8080", "server base URL")
	flag.Parse()

	path := *session
	if path == "" {
		var err error
		path, err = latestSessionFile()
		if err != nil {
			log.Fatalf("auto-detect: %v", err)
		}
		log.Printf("watching: %s", path)
	}

	for {
		if err := tailFile(path, *server); err != nil {
			log.Printf("tail error: %v — retrying in 3s", err)
			time.Sleep(3 * time.Second)
		}
	}
}
