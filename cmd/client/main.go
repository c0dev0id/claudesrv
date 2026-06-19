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
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/c0dev0id/claude-live-coding/internal/event"
)

// rawEntry is the top-level structure of a JSONL line.
type rawEntry struct {
	Type    string      `json:"type"`
	IsMeta  bool        `json:"isMeta"`
	Message *rawMessage `json:"message"`
}

type rawMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string or []contentBlock
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
}

func parseEntry(line []byte) []event.Event {
	var entry rawEntry
	if err := json.Unmarshal(line, &entry); err != nil || entry.Message == nil {
		return nil
	}

	msg := entry.Message
	now := time.Now().UnixMilli()

	switch entry.Type {
	case "user":
		if entry.IsMeta {
			return nil
		}
		return parseUserContent(msg.Content, now)

	case "assistant":
		return parseAssistantContent(msg.Content, now)
	}
	return nil
}

func parseUserContent(raw json.RawMessage, ts int64) []event.Event {
	// content can be a plain string (user typed) or an array of blocks (tool results, etc.)
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if strings.HasPrefix(s, "<") {
			return nil // slash command or system XML
		}
		return []event.Event{{Kind: event.KindUser, Text: s, Ts: ts}}
	}

	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && !strings.HasPrefix(b.Text, "<") {
			parts = append(parts, b.Text)
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return []event.Event{{Kind: event.KindUser, Text: strings.Join(parts, "\n"), Ts: ts}}
}

func parseAssistantContent(raw json.RawMessage, ts int64) []event.Event {
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
		}
	}
	return events
}

func summarizeInput(toolName string, raw json.RawMessage) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return toolName
	}
	// prefer the most descriptive key for each tool type
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

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)

	sendLine := func(line []byte) {
		events := parseEntry(line)
		for _, e := range events {
			postWithRetry(server, e)
		}
	}

	// replay existing content
	for scanner.Scan() {
		sendLine(scanner.Bytes())
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	// stream new content
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
						sendLine(bytes.TrimRight(line, "\n"))
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
