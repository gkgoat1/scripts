package prtag

import "testing"

func TestParse_MinimalHeaderOnly(t *testing.T) {
	f, err := Parse([]byte("proj:\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Name != "proj" {
		t.Fatalf("Name = %q, want %q", f.Name, "proj")
	}
	if f.Body != "" {
		t.Fatalf("Body = %q, want empty", f.Body)
	}
	if f.MetaHeader != "" {
		t.Fatalf("MetaHeader = %q, want empty", f.MetaHeader)
	}
}

func TestParse_WithBody_NoDelimiter(t *testing.T) {
	in := "proj:\nhello\nworld\n"
	f, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Name != "proj" {
		t.Fatalf("Name = %q, want %q", f.Name, "proj")
	}
	if f.Body != "hello\nworld\n" {
		t.Fatalf("Body = %q, want %q", f.Body, "hello\nworld\n")
	}
	if f.MetaHeader != "" {
		t.Fatalf("MetaHeader = %q, want empty", f.MetaHeader)
	}
}

func TestParse_WithDelimiterAndSection_EmptyMetadata(t *testing.T) {
	in := "proj:\nhello\n---\n[anything]\n\n"
	f, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Name != "proj" {
		t.Fatalf("Name = %q, want %q", f.Name, "proj")
	}
	if f.Body != "hello\n" {
		t.Fatalf("Body = %q, want %q", f.Body, "hello\n")
	}
	if f.MetaHeader != "anything" {
		t.Fatalf("MetaHeader = %q, want %q", f.MetaHeader, "anything")
	}
}

func TestParse_Errors(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"missing_header", "nope\n"},
		{"empty_header", ":\n"},
		{"delimiter_missing_section", "proj:\n---\n"},
		{"delimiter_bad_section", "proj:\n---\nmetadata\n"},
		{"non_empty_metadata", "proj:\n---\n[metadata]\nkey: value\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.in))
			if err == nil {
				t.Fatalf("Parse: expected error")
			}
		})
	}
}

func TestFormat_AlwaysEmitsDelimiterAndHeader(t *testing.T) {
	b, err := Format(File{Name: "proj", Body: "hello"})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	want := "proj:\nhello\n---\n[metadata]\n"
	if string(b) != want {
		t.Fatalf("Format = %q, want %q", string(b), want)
	}
}

func TestRoundTrip_Canonicalizes(t *testing.T) {
	// No delimiter on input, but writer should add it.
	in := "proj:\nhello\n"
	f, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out, err := Format(f)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	want := "proj:\nhello\n---\n[metadata]\n"
	if string(out) != want {
		t.Fatalf("out = %q, want %q", string(out), want)
	}
}

