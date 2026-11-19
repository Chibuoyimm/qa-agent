package repository

import (
	"context"
	"errors"
	"path"
	"sort"
	"strings"
	"time"
)

var ErrInventoryLimit = errors.New("repository file search exceeds its safe limit; choose exact files manually")

type Candidate struct {
	Path string `json:"path"`
	Size int    `json:"size"`
}
type Inventory struct {
	blobs     map[string]string
	CommitSHA string      `json:"commit_sha"`
	Files     []Candidate `json:"files"`
	Partial   bool        `json:"partial,omitempty"`
	Excluded  int         `json:"excluded"`
}

// Inventory reads names and sizes only. A complete, bounded traversal precedes
// model selection; generated directories, secrets, oversized files and binary
// formats are excluded. No partial tree is represented as a complete search.
func (c *Client) Inventory(ctx context.Context, in Input, token string) (Inventory, error) {
	return c.inventory(ctx, in, token, false)
}

func (c *Client) SurveyInventory(ctx context.Context, in Input, token string) (Inventory, error) {
	return c.inventory(ctx, in, token, true)
}
func (c *Client) Resolve(ctx context.Context, in Input, token string) error {
	if err := validateConnection(in, token); err != nil {
		return err
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		return ErrBusy
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	in.Provider = EffectiveProvider(in.Provider)
	base := in.apiBase()
	budget := requestBudget{}
	_, _, err := c.resolveCommit(ctx, base, in, token, &budget)
	return err
}
func (c *Client) inventory(ctx context.Context, in Input, token string, allowPartial bool) (Inventory, error) {
	if err := validateConnection(in, token); err != nil {
		return Inventory{}, err
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		return Inventory{}, ErrBusy
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	in.Provider = EffectiveProvider(in.Provider)
	base := in.apiBase()
	budget := requestBudget{}
	commit, root, err := c.resolveCommit(ctx, base, in, token, &budget)
	if err != nil {
		return Inventory{}, err
	}
	result := Inventory{CommitSHA: commit, Files: []Candidate{}, blobs: map[string]string{}}
	recursive := allowPartial && in.Provider == "github"
	type directory struct {
		prefix, sha string
		depth       int
	}
	pending := []directory{{sha: root}}
	bytes := 0
scan:
	for len(pending) > 0 {
		dir := pending[0]
		pending = pending[1:]
		if budget.requests >= maxAPIRequests || dir.depth > 19 {
			if allowPartial {
				result.Partial = true
				break scan
			}
			return Inventory{}, ErrInventoryLimit
		}
		var tree gitTree
		if recursive {
			err = c.get(ctx, base+"/git/trees/"+root+"?recursive=1", token, maxMetadataBytes/2, &budget, &tree)
			if errors.Is(err, errResponseTooLarge) {
				// Spend the remaining metadata budget walking individual directories.
				recursive = false
				pending = append([]directory{dir}, pending...)
				continue
			}
		} else {
			tree, err = c.fetchTree(ctx, base, in.Provider, dir.sha, token, &budget)
		}
		if err != nil {
			return Inventory{}, err
		}
		if tree.Truncated && recursive {
			result.Partial = true
		}
		if (tree.Truncated && !recursive) || tree.SHA != dir.sha {
			return Inventory{}, ErrUpstream
		}
		seen := map[string]bool{}
		for _, entry := range tree.Tree {
			if entry.Path == "" || (!recursive && strings.Contains(entry.Path, "/")) || seen[entry.Path] || !shaPattern.MatchString(entry.SHA) {
				return Inventory{}, ErrUpstream
			}
			seen[entry.Path] = true
			filename := dir.prefix + entry.Path
			if validatePaths([]string{filename}) != nil || excludedInventoryPath(entry.Path) {
				result.Excluded++
				continue
			}
			if entry.Type == "tree" && (entry.Mode == "040000" || entry.Mode == "40000") {
				if recursive {
					continue
				}
				pending = append(pending, directory{filename + "/", entry.SHA, dir.depth + 1})
				if len(pending) > maxAPIRequests {
					if allowPartial {
						result.Partial = true
						pending = pending[:maxAPIRequests]
						continue
					}
					return Inventory{}, ErrInventoryLimit
				}
				continue
			}
			if entry.Type != "blob" || (entry.Mode != "100644" && entry.Mode != "100755") || entry.Size < 0 || entry.Size > maxBytes || !sourceName(path.Base(entry.Path)) {
				result.Excluded++
				continue
			}
			bytes += len(filename) + 40
			if len(result.Files) >= 2000 || bytes > 55000 {
				if allowPartial {
					result.Partial = true
					break scan
				}
				return Inventory{}, ErrInventoryLimit
			}
			result.Files = append(result.Files, Candidate{filename, entry.Size})
			result.blobs[filename] = entry.SHA
		}
	}
	if len(result.Files) == 0 && !allowPartial {
		return Inventory{}, ErrInvalid
	}
	sort.Slice(result.Files, func(i, j int) bool { return result.Files[i].Path < result.Files[j].Path })
	return result, nil
}

func excludedInventoryPath(filename string) bool {
	for _, part := range strings.Split(filename, "/") {
		if excludedName(part) {
			return true
		}
	}
	return false
}

func excludedName(name string) bool {
	switch strings.ToLower(name) {
	case ".git", "node_modules", "vendor", "dist", "build", "coverage", ".next", ".nuxt", ".venv", "venv", "__pycache__", "artifacts", ".cache":
		return true
	}
	return false
}
func sourceName(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".py", ".rb", ".rs", ".java", ".kt", ".cs", ".php", ".swift", ".vue", ".svelte", ".html", ".css", ".scss", ".sql", ".graphql", ".gql", ".json", ".yaml", ".yml", ".toml", ".md", ".txt", ".xml", ".sh", ".feature":
		return !strings.HasSuffix(strings.ToLower(name), ".min.js") && !strings.HasSuffix(strings.ToLower(name), ".min.css") && name != "package-lock.json"
	}
	return name == "Dockerfile" || name == "Makefile" || name == "go.mod"
}
