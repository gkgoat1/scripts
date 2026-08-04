package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gkgoat1/scripts/agentweave/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type testBackend struct{ dossier core.EvidenceDossier }

func (b testBackend) Search(context.Context, core.SearchRequest) ([]core.SearchResult, error) {
	return nil, nil
}
func (b testBackend) Read(context.Context, string, []string, int) ([]core.SearchResult, error) {
	return nil, nil
}
func (b testBackend) Dossier(context.Context, core.SynthesisRequest) (core.EvidenceDossier, error) {
	return b.dossier, nil
}
func (b testBackend) Status(context.Context) ([]core.SourceStatus, error) { return nil, nil }

func TestValidateCitations(t *testing.T) {
	evidence := []core.SearchResult{{Ref: "aw:one:0"}}
	if err := ValidateCitations("Supported [aw:one:0]", evidence); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCitations("Unsupported [aw:other:0]", evidence); err == nil {
		t.Fatal("accepted citation outside evidence bundle")
	}
	if err := ValidateCitations("No citation", evidence); err == nil {
		t.Fatal("accepted uncited sampled answer")
	}
}

func TestSynthesisSamplingIsOneRequestAndEvidenceModeNeverSamples(t *testing.T) {
	ctx := context.Background()
	evidence := []core.SearchResult{{Ref: "aw:one:0", Excerpt: "Decision: add tests."}}
	backend := testBackend{dossier: core.EvidenceDossier{Question: "what", Evidence: evidence, Prompt: "Use [aw:one:0] only."}}
	server := New(backend, "test")
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	requests := 0
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, &mcp.ClientOptions{
		Capabilities: &mcp.ClientCapabilities{Sampling: &mcp.SamplingCapabilities{}},
		CreateMessageHandler: func(_ context.Context, request *mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
			requests++
			if request.Params.MaxTokens != 1600 || len(request.Params.Messages) != 1 || !strings.Contains(request.Params.Messages[0].Content.(*mcp.TextContent).Text, "aw:one:0") {
				t.Fatalf("unexpected sampling request: %#v", request.Params)
			}
			return &mcp.CreateMessageResult{Model: "parent-model", Role: "assistant", Content: &mcp.TextContent{Text: "The decision was to add tests. [aw:one:0]"}}, nil
		},
	})
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	call := func(generation string) *mcp.CallToolResult {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "agentweave_synthesize", Arguments: map[string]any{"workspace": "/work/demo", "question": "what", "generation": generation}})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	evidenceResult := call("evidence")
	if evidenceResult.IsError || requests != 0 {
		t.Fatalf("evidence result=%#v sampling requests=%d", evidenceResult, requests)
	}
	sampleResult := call("sample")
	if sampleResult.IsError || requests != 1 {
		t.Fatalf("sample result=%#v sampling requests=%d", sampleResult, requests)
	}
	text := sampleResult.Content[0].(*mcp.TextContent).Text
	var returned struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal([]byte(text), &returned); err != nil || !strings.Contains(returned.Answer, "[aw:one:0]") {
		t.Fatalf("sample result text=%q err=%v", text, err)
	}
}
