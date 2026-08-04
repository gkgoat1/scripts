package core

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxJSONLLine = 4 * 1024 * 1024

type conversationMeta struct {
	Workspace string
	SessionID string
	CreatedAt time.Time
	UpdatedAt time.Time
	Title     string
	ParentID  string
}

func parseJSONLConversation(path string, agent Agent, workspace string) (Artifact, error) {
	f, err := os.Open(path)
	if err != nil {
		return Artifact{}, err
	}
	defer f.Close()

	meta := conversationMeta{Workspace: CanonicalWorkspace(workspace)}
	var lines []string
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, maxJSONLLine)
	ordinal := 0
	for scanner.Scan() {
		ordinal++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var value any
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			// Active writers may leave an incomplete final line. Preserve all
			// earlier records and do not fail the complete conversation.
			continue
		}
		obj, _ := value.(map[string]any)
		mergeConversationMeta(&meta, obj)
		role, text := eventText(obj)
		if text == "" {
			continue
		}
		if role == "" {
			role = "record"
		}
		if meta.Title == "" && role == "user" {
			meta.Title = firstLine(text, 120)
		}
		lines = append(lines, fmt.Sprintf("[%s #%d]\n%s", role, ordinal, text))
	}
	if err := scanner.Err(); err != nil {
		return Artifact{}, fmt.Errorf("read JSONL %s: %w", path, err)
	}
	if len(lines) == 0 {
		return Artifact{}, fmt.Errorf("no conversation text in %s", path)
	}
	info, _ := os.Stat(path)
	updated := time.Now().UTC()
	if info != nil {
		updated = info.ModTime().UTC()
	}
	if !meta.UpdatedAt.IsZero() {
		updated = meta.UpdatedAt
	}
	created := updated
	if !meta.CreatedAt.IsZero() {
		created = meta.CreatedAt
	}
	return Artifact{
		ID:           StableID(string(agent), path, meta.SessionID),
		Agent:        agent,
		Kind:         KindConversation,
		Workspace:    meta.Workspace,
		Title:        meta.Title,
		SourcePath:   path,
		SourceRecord: meta.SessionID,
		ParentID:     meta.ParentID,
		CreatedAt:    created,
		UpdatedAt:    updated,
		Text:         strings.Join(lines, "\n\n"),
	}, nil
}

func mergeConversationMeta(meta *conversationMeta, obj map[string]any) {
	if obj == nil {
		return
	}
	for _, key := range []string{"cwd", "workspace", "workspacePath", "projectPath", "working_directory"} {
		if value, ok := obj[key].(string); ok && meta.Workspace == "" {
			meta.Workspace = CanonicalWorkspace(value)
		}
	}
	for _, key := range []string{"sessionId", "session_id", "thread_id", "threadId", "id"} {
		if value, ok := obj[key].(string); ok && meta.SessionID == "" {
			meta.SessionID = value
		}
	}
	for _, key := range []string{"parentId", "parent_id", "parentThreadId"} {
		if value, ok := obj[key].(string); ok && meta.ParentID == "" {
			meta.ParentID = value
		}
	}
	for _, key := range []string{"timestamp", "created_at", "createdAt"} {
		if value, ok := obj[key].(string); ok && meta.CreatedAt.IsZero() {
			meta.CreatedAt = parseTime(value)
		}
	}
	for _, key := range []string{"updated_at", "updatedAt", "timestamp"} {
		if value, ok := obj[key].(string); ok {
			if parsed := parseTime(value); !parsed.IsZero() {
				meta.UpdatedAt = parsed
			}
		}
	}
}

func eventText(obj map[string]any) (string, string) {
	if obj == nil {
		return "", ""
	}
	role, _ := obj["role"].(string)
	if message, ok := obj["message"].(map[string]any); ok {
		if r, ok := message["role"].(string); ok {
			role = r
		}
		if text := contentText(message["content"]); text != "" {
			return role, text
		}
	}
	if text := contentText(obj["content"]); text != "" {
		return role, text
	}
	if text := contentText(obj["text"]); text != "" {
		return role, text
	}
	// A few VS Code-derived formats keep the payload below value.
	if value, ok := obj["value"].(map[string]any); ok {
		if r, text := eventText(value); text != "" {
			if r == "" {
				r = role
			}
			return r, text
		}
	}
	return role, ""
}

func contentText(value any) string {
	switch value := value.(type) {
	case string:
		return strings.TrimSpace(value)
	case []any:
		var parts []string
		for _, item := range value {
			if text := contentText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		if kind, _ := value["type"].(string); kind != "" && kind != "text" && kind != "input_text" {
			return ""
		}
		if text, ok := value["text"].(string); ok {
			return strings.TrimSpace(text)
		}
		if text, ok := value["value"].(string); ok {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func parseTime(value string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func firstLine(value string, max int) string {
	value = strings.TrimSpace(strings.Split(value, "\n")[0])
	if len(value) > max {
		return value[:max-1] + "…"
	}
	return value
}

func parseMarkdown(path string, agent Agent, kind Kind, workspace string) (Artifact, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Artifact{}, err
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return Artifact{}, fmt.Errorf("empty markdown artifact")
	}
	info, _ := os.Stat(path)
	updated := time.Now().UTC()
	if info != nil {
		updated = info.ModTime().UTC()
	}
	title := firstLine(strings.TrimPrefix(text, "#"), 120)
	return Artifact{Agent: agent, Kind: kind, Workspace: workspace, Title: title, SourcePath: path, UpdatedAt: updated, Text: text}, nil
}

// parseJSONArtifact is intentionally conservative: it extracts only common
// human-authored task fields, rather than serializing an entire record that
// could include transport metadata or secrets.
func parseJSONArtifact(path string, agent Agent, kind Kind, workspace string) (Artifact, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Artifact{}, err
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return Artifact{}, err
	}
	text := strings.TrimSpace(jsonArtifactText(value))
	if text == "" {
		return Artifact{}, fmt.Errorf("no task text in %s", path)
	}
	info, _ := os.Stat(path)
	updated := time.Now().UTC()
	if info != nil {
		updated = info.ModTime().UTC()
	}
	return Artifact{Agent: agent, Kind: kind, Workspace: CanonicalWorkspace(workspace), Title: firstLine(text, 120), SourcePath: path, UpdatedAt: updated, Text: text}, nil
}

func jsonArtifactText(value any) string {
	allowed := map[string]bool{"title": true, "description": true, "objective": true, "content": true, "summary": true, "plan": true, "status": true, "task": true}
	var values []string
	var walk func(any, string)
	walk = func(current any, key string) {
		switch item := current.(type) {
		case string:
			if allowed[strings.ToLower(key)] && strings.TrimSpace(item) != "" {
				values = append(values, strings.TrimSpace(item))
			}
		case []any:
			for _, child := range item {
				walk(child, key)
			}
		case map[string]any:
			for childKey, child := range item {
				walk(child, childKey)
			}
		}
	}
	walk(value, "")
	return strings.Join(values, "\n")
}

func fileFingerprint(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return StableID(string(data), fmt.Sprintf("%d", len(data))), nil
}

func artifactChunks(artifact Artifact, maxBytes int) []Chunk {
	if maxBytes <= 0 {
		maxBytes = 3200
	}
	var chunks []Chunk
	remaining := artifact.Text
	for ordinal := 0; remaining != ""; ordinal++ {
		cut := len(remaining)
		if cut > maxBytes {
			cut = strings.LastIndex(remaining[:maxBytes], "\n")
			if cut < maxBytes/2 {
				cut = maxBytes
			}
		}
		body := strings.TrimSpace(remaining[:cut])
		remaining = strings.TrimLeft(remaining[cut:], "\n")
		if body == "" {
			continue
		}
		chunks = append(chunks, Chunk{Ref: fmt.Sprintf("aw:%s:%d", artifact.ID, ordinal), ArtifactID: artifact.ID, Ordinal: ordinal, Body: body})
	}
	return chunks
}

func workspaceFromCursorStorage(storageDir string) string {
	data, err := os.ReadFile(filepath.Join(storageDir, "workspace.json"))
	if err != nil {
		return ""
	}
	var workspace struct {
		Folder string `json:"folder"`
	}
	if json.Unmarshal(data, &workspace) != nil {
		return ""
	}
	return CanonicalWorkspace(workspace.Folder)
}
