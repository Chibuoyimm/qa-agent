package repository

import (
	"context"
	"crypto/sha1"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// A repository can contribute more than twenty files while every individual read stays bounded.
func TestSurveyAllocatesAllSelectedFiles(t *testing.T) {
	inventory := Inventory{blobs: map[string]string{}}
	var paths []string
	for i := 0; i < 41; i++ {
		path := fmt.Sprintf("area/file_%d.ts", i)
		paths = append(paths, path)
		size := 1000
		if i == 40 {
			size = 40000
		}
		inventory.Files = append(inventory.Files, Candidate{Path: path, Size: size})
		inventory.blobs[path] = strings.Repeat("a", 40)
	}
	batches, err := inventory.SurveyBatches(paths)
	if err != nil || len(batches) != 3 || len(batches[0]) != 20 || len(batches[1]) != 20 || len(batches[2]) != 1 {
		t.Fatalf("bad allocation: %v %v", batches, err)
	}
	for _, batch := range batches {
		for _, f := range batch {
			if f.Path != paths[0] {
				t.Fatal("lost source order")
			}
			paths = paths[1:]
		}
	}
	if len(paths) != 0 {
		t.Fatal("selected files lost")
	}
}
func TestSurveyChecksPublicContentAgainstPinnedBlob(t *testing.T) {
	body := "export const receipt = true"
	client := New(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "raw.githubusercontent.com" || r.URL.Path != "/shop/app/"+commitSHA+"/ui/receipt.ts" || r.Header.Get("Authorization") != "" {
			t.Fatal("unexpected public content request")
		}
		return response(200, body), nil
	}))
	src := Input{Provider: "github", Repository: "shop/app", Ref: commitSHA, Role: "frontend"}
	file := SurveyFile{Path: "ui/receipt.ts", Size: len(body), SHA: strings.Repeat("1", 40)}
	if _, _, err := client.ReadSurveyBatch(context.Background(), src, "", []SurveyFile{file}); err == nil {
		t.Fatal("accepted content with the wrong blob identifier")
	}
	body = "password = 'example-test-secret-value'"
	file.Size = len(body)
	file.SHA = fmt.Sprintf("%x", sha1.Sum([]byte(fmt.Sprintf("blob %d\x00%s", len(body), body))))
	files, excluded, err := client.ReadSurveyBatch(context.Background(), src, "", []SurveyFile{file})
	if err != nil || len(files) != 0 || excluded != 1 {
		t.Fatalf("filtered source was exposed: %v %d", err, excluded)
	}
}
