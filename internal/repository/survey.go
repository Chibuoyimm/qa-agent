package repository

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/url"
	"time"
)

// A survey retains object identities from the pinned tree across model calls.
type SurveyFile struct {
	Path string `json:"path"`
	Size int    `json:"size"`
	SHA  string `json:"sha"`
}

func (inventory Inventory) SurveyBatches(paths []string) ([][]SurveyFile, error) {
	sizes := make(map[string]int, len(inventory.Files))
	for _, f := range inventory.Files {
		sizes[f.Path] = f.Size
	}
	var batches [][]SurveyFile
	var batch []SurveyFile
	size := 0
	for _, path := range paths {
		n, ok := sizes[path]
		sha := inventory.blobs[path]
		if !ok || n < 0 || n > maxBytes || !shaPattern.MatchString(sha) {
			return nil, ErrInvalid
		}
		if len(batch) == maxFiles || size+n > maxBytes {
			batches = append(batches, batch)
			batch = nil
			size = 0
		}
		batch = append(batch, SurveyFile{path, n, sha})
		size += n
	}
	if len(batch) > 0 {
		batches = append(batches, batch)
	}
	return batches, nil
}

// Public GitHub content uses its immutable raw URL. Credentials only go to the provider API.
func (c *Client) ReadSurveyBatch(ctx context.Context, in Input, token string, batch []SurveyFile) ([]File, int, error) {
	if err := validateConnection(in, token); err != nil {
		return nil, 0, err
	}
	if !shaPattern.MatchString(in.Ref) || len(batch) == 0 || len(batch) > maxFiles {
		return nil, 0, ErrInvalid
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		return nil, 0, ErrBusy
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	total := 0
	excluded := 0
	result := make([]File, 0, len(batch))
	budget := requestBudget{}
	for _, f := range batch {
		if validatePaths([]string{f.Path}) != nil || !shaPattern.MatchString(f.SHA) || f.Size < 0 || f.Size > maxBytes-total {
			return nil, 0, ErrInvalid
		}
		total += f.Size
		var data []byte
		var err error
		if in.Provider == "github" && token == "" {
			raw := url.URL{Scheme: "https", Host: "raw.githubusercontent.com", Path: "/" + in.Repository + "/" + in.Ref + "/" + f.Path}
			data, err = c.read(ctx, raw.String(), "", "application/octet-stream", int64(f.Size), &budget)
			if err == nil {
				h := sha1.New()
				fmt.Fprintf(h, "blob %d\x00", len(data))
				h.Write(data)
				if len(data) != f.Size || hex.EncodeToString(h.Sum(nil)) != f.SHA {
					return nil, 0, ErrUpstream
				}
			}
		} else {
			data, err = c.fetchBlob(ctx, in.apiBase(), in.Provider, treeEntry{SHA: f.SHA, Size: f.Size}, token, &budget)
		}
		if err != nil {
			return nil, 0, err
		}
		if !safeText(data) {
			excluded++
			continue
		}
		result = append(result, File{Path: f.Path, Content: string(data)})
	}
	return result, excluded, nil
}
