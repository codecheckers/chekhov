package check

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// The one test that writes: it makes an unpublished draft on the Zenodo
// sandbox, shares it, audits it the way `check zenodo` does with a share link
// somebody pasted, and deletes it. Offline, a draft is a fixture written from
// InvenioRDM's documentation; this is the only place the draft route, its
// files listing beside it and the share link's token are read from Zenodo
// itself. It runs when the integration suite does and a sandbox token is set:
//
//	CHEKHOV_INTEGRATION=1 CHEKHOV_ZENODO_SANDBOX_TOKEN=... go test ./internal/check/ -run Sandbox
//
// The token never reaches the bot: a deployment reads a draft with the share
// link it is given and holds no Zenodo credential of its own. See
// docs/zenodo-sandbox-token.md.
//
// CHEKHOV_ZENODO_KEEP_DRAFT=1 leaves the draft in place and logs its share
// link, for a live test of the deployment.

// sandboxDraft is a draft the test made, and the share link to it.
type sandboxDraft struct {
	ID, Link string
}

// sandboxClient writes to the sandbox with the token, and only there.
type sandboxClient struct {
	api, token string
	http       *http.Client
}

func (c sandboxClient) call(t *testing.T, method, path string, body any, contentType string) map[string]any {
	t.Helper()
	var reader io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		reader = bytes.NewReader(b)
	default:
		encoded, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(context.Background(), method, c.api+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		// Go's error quotes the URL, which carries no token: the token is in
		// a header.
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	answer, _ := io.ReadAll(response.Body)
	if response.StatusCode >= 300 {
		t.Fatalf("%s %s: the sandbox answered %d: %.300s", method, path, response.StatusCode, answer)
	}
	parsed := map[string]any{}
	if len(answer) > 0 {
		_ = json.Unmarshal(answer, &parsed)
	}
	return parsed
}

// makeSandboxDraft makes a draft that follows the curation policy, as a
// codechecker would have it before publishing, with a certificate PDF and its
// source, and a share link to it.
func makeSandboxDraft(t *testing.T, c sandboxClient) sandboxDraft {
	t.Helper()
	metadata := map[string]any{
		"title":            "CODECHECK Certificate 1970-001",
		"description":      "<p>A test draft of chekhov's integration suite. It is deleted after the test and never published.</p>",
		"publication_date": time.Now().Format("2006-01-02"),
		"publisher":        policyPublisher,
		"resource_type":    map[string]string{"id": policyResourceType},
		"languages":        []map[string]string{{"id": "eng"}},
		"rights":           []map[string]string{{"id": ccBy4}},
		"creators": []map[string]any{{"person_or_org": map[string]string{
			"type": "personal", "given_name": "A", "family_name": "Codechecker"}}},
		"related_identifiers": []map[string]any{
			{"identifier": "10.5555/preprint.1", "scheme": "doi",
				"relation_type": map[string]string{"id": "reviews"}, "resource_type": map[string]string{"id": "publication-preprint"}},
			{"identifier": "https://github.com/codecheckers/demo", "scheme": "url",
				"relation_type": map[string]string{"id": "issupplementto"}, "resource_type": map[string]string{"id": "software"}},
		},
		"identifiers": []map[string]string{
			{"identifier": "http://" + certificatesURL + "1970-001", "scheme": "url"},
			{"identifier": certificatesURL + "1970-001", "scheme": "other"},
		},
	}
	draft := c.call(t, http.MethodPost, "/records", map[string]any{
		"metadata": metadata,
		"files":    map[string]bool{"enabled": true},
		"access":   map[string]string{"record": "public", "files": "public"},
	}, "application/json")
	id, _ := draft["id"].(string)
	if id == "" {
		t.Fatalf("the sandbox made a draft with no id: %v", draft)
	}
	if os.Getenv("CHEKHOV_ZENODO_KEEP_DRAFT") == "" {
		t.Cleanup(func() { c.call(t, http.MethodDelete, "/records/"+id+"/draft", nil, "application/json") })
	}

	files := map[string][]byte{
		"codecheck.pdf": []byte("%PDF-1.1\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj 2 0 obj<</Type/Pages/Kids[]/Count 0>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n"),
		"codecheck.tex": []byte("\\documentclass{article}\\begin{document}A test.\\end{document}\n"),
	}
	var keys []map[string]string
	for key := range files {
		keys = append(keys, map[string]string{"key": key})
	}
	c.call(t, http.MethodPost, "/records/"+id+"/draft/files", keys, "application/json")
	for key, content := range files {
		c.call(t, http.MethodPut, "/records/"+id+"/draft/files/"+key+"/content", content, "application/octet-stream")
		c.call(t, http.MethodPost, "/records/"+id+"/draft/files/"+key+"/commit", nil, "application/json")
	}

	// Choosing the community makes the request without submitting it, so
	// nobody who curates the sandbox community is asked to review a test.
	c.call(t, http.MethodPut, "/records/"+id+"/draft/review", map[string]any{
		"type": "community-submission", "receiver": map[string]string{"community": zenodoSandboxCommunityID},
	}, "application/json")

	link := c.call(t, http.MethodPost, "/records/"+id+"/access/links", map[string]string{"permission": "preview"}, "application/json")
	token, _ := link["token"].(string)
	if token == "" {
		t.Fatal("the sandbox made a share link with no token")
	}
	return sandboxDraft{ID: id, Link: fmt.Sprintf("https://sandbox.zenodo.org/records/%s?preview=1&token=%s", id, token)}
}

func TestIntegrationSandboxDraftByShareLink(t *testing.T) {
	services := integrationServices(t)
	token := strings.TrimSpace(os.Getenv("CHEKHOV_ZENODO_SANDBOX_TOKEN"))
	if token == "" {
		t.Skip("set CHEKHOV_ZENODO_SANDBOX_TOKEN to audit a draft on the Zenodo sandbox")
	}
	draft := makeSandboxDraft(t, sandboxClient{api: services.ZenodoSandbox, token: token, http: services.HTTP})
	if os.Getenv("CHEKHOV_ZENODO_KEEP_DRAFT") != "" {
		t.Logf("kept the draft %s; share link: %s", draft.ID, draft.Link)
	}

	target, err := ParseZenodoTarget(draft.Link)
	if err != nil {
		t.Fatal(err)
	}
	audit := AuditZenodo(context.Background(), target, Context{}, services)
	if audit.Problem != "" {
		t.Fatalf("Problem = %q", audit.Problem)
	}
	if !audit.Draft || !audit.Shared {
		t.Errorf("Draft, Shared = %v, %v; want a draft read with its share link", audit.Draft, audit.Shared)
	}
	for _, item := range audit.Policy {
		if item.Status != StatusPass {
			t.Errorf("%s: %s %s, want pass", item.What, item.Status, item.Detail)
		}
	}

	// Without the token the draft is not there, and the reply says how to
	// show it.
	target.Token = ""
	if hidden := AuditZenodo(context.Background(), target, Context{}, services); !strings.Contains(hidden.Problem, "share link") {
		t.Errorf("without the token: Problem = %q, want the hint to use a share link", hidden.Problem)
	}
}
