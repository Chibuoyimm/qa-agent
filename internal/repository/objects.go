package repository

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

// Azure Services uses a fixed origin; user-provided clone URLs are never fetched.
func azureBase(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "dev.azure.com" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", fmt.Errorf("%w: use https://dev.azure.com/organization/project/_git/repository", ErrInvalid)
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) != 5 || parts[0] != "" || parts[3] != "_git" || len(parts[1]) > 50 || !repositoryPart.MatchString(parts[1]) {
		return "", fmt.Errorf("%w: invalid Azure repository URL", ErrInvalid)
	}
	for _, part := range []string{parts[2], parts[4]} {
		if part == "" || part == "." || part == ".." || len(part) > 200 || !utf8.ValidString(part) || strings.TrimSpace(part) != part || strings.ContainsAny(part, "/\\%") {
			return "", fmt.Errorf("%w: invalid Azure project or repository name", ErrInvalid)
		}
		for _, r := range part {
			if r < 0x20 || r == 0x7f {
				return "", fmt.Errorf("%w: invalid Azure project or repository name", ErrInvalid)
			}
		}
	}
	return "https://dev.azure.com/" + url.PathEscape(parts[1]) + "/" + url.PathEscape(parts[2]) + "/_apis/git/repositories/" + url.PathEscape(parts[4]), nil
}

func (c *Client) resolveCommit(ctx context.Context, base string, in Input, token string, budget *requestBudget) (string, string, error) {
	if in.Provider == "github" {
		var commit struct {
			SHA    string `json:"sha"`
			Commit struct {
				Tree struct {
					SHA string `json:"sha"`
				} `json:"tree"`
			} `json:"commit"`
		}
		if err := c.get(ctx, base+"/commits/"+url.PathEscape(in.Ref), token, 1_000_000, budget, &commit); err != nil {
			return "", "", err
		}
		if !shaPattern.MatchString(commit.SHA) || !shaPattern.MatchString(commit.Commit.Tree.SHA) {
			return "", "", fmt.Errorf("%w: invalid commit metadata", ErrUpstream)
		}
		return commit.SHA, commit.Commit.Tree.SHA, nil
	}
	sha := in.Ref
	if !shaPattern.MatchString(sha) {
		name := in.Ref
		if !strings.HasPrefix(name, "refs/") {
			name = "refs/heads/" + name
		}
		if !strings.HasPrefix(name, "refs/heads/") && !strings.HasPrefix(name, "refs/tags/") {
			return "", "", fmt.Errorf("%w: use a branch, refs/tags/name, or full commit SHA", ErrInvalid)
		}
		var refs struct {
			Value []struct {
				Name           string `json:"name"`
				ObjectID       string `json:"objectId"`
				PeeledObjectID string `json:"peeledObjectId"`
			} `json:"value"`
		}
		query := url.Values{"api-version": {"7.1"}, "filter": {strings.TrimPrefix(name, "refs/")}, "peelTags": {"true"}, "$top": {"1000"}}
		if err := c.get(ctx, base+"/refs?"+query.Encode(), token, 1_000_000, budget, &refs); err != nil {
			return "", "", err
		}
		sha = ""
		for _, ref := range refs.Value {
			// The upstream filter matches prefixes, so main-old cannot stand in for main.
			if ref.Name != name {
				continue
			}
			if sha != "" {
				return "", "", fmt.Errorf("%w: duplicate ref metadata", ErrUpstream)
			}
			sha = ref.ObjectID
			if ref.PeeledObjectID != "" {
				sha = ref.PeeledObjectID
			}
			if !shaPattern.MatchString(sha) || sha == strings.Repeat("0", 40) {
				return "", "", fmt.Errorf("%w: invalid ref metadata", ErrUpstream)
			}
		}
		if sha == "" {
			return "", "", fmt.Errorf("%w: exact ref missing from bounded provider response; use a full commit SHA", ErrInvalid)
		}
	}
	var commit struct {
		CommitID string `json:"commitId"`
		TreeID   string `json:"treeId"`
	}
	if err := c.get(ctx, base+"/commits/"+sha+"?api-version=7.1&changeCount=0", token, 1_000_000, budget, &commit); err != nil {
		return "", "", err
	}
	if !strings.EqualFold(commit.CommitID, sha) || !shaPattern.MatchString(commit.TreeID) {
		return "", "", fmt.Errorf("%w: inconsistent commit metadata", ErrUpstream)
	}
	return commit.CommitID, commit.TreeID, nil
}

func (c *Client) fetchTree(ctx context.Context, base, provider, sha, token string, budget *requestBudget) (gitTree, error) {
	var tree gitTree
	if provider == "github" {
		err := c.get(ctx, base+"/git/trees/"+sha, token, 2_000_000, budget, &tree)
		return tree, err
	}
	var azure struct {
		ObjectID    string `json:"objectId"`
		TreeEntries []struct {
			Path string `json:"relativePath"`
			Mode string `json:"mode"`
			Type string `json:"gitObjectType"`
			Size int    `json:"size"`
			SHA  string `json:"objectId"`
		} `json:"treeEntries"`
	}
	if err := c.get(ctx, base+"/trees/"+sha+"?api-version=7.1&recursive=false", token, 2_000_000, budget, &azure); err != nil {
		return tree, err
	}
	tree.SHA = azure.ObjectID
	for _, entry := range azure.TreeEntries {
		tree.Tree = append(tree.Tree, treeEntry{Path: entry.Path, Mode: entry.Mode, Type: entry.Type, Size: entry.Size, SHA: entry.SHA})
	}
	return tree, nil
}

func (c *Client) fetchBlob(ctx context.Context, base, provider string, entry treeEntry, token string, budget *requestBudget) ([]byte, error) {
	if provider == "azure" {
		content, err := c.read(ctx, base+"/blobs/"+entry.SHA+"?api-version=7.1&$format=octetstream&resolveLfs=false", token, "application/octet-stream", int64(entry.Size), budget)
		if err != nil {
			return nil, err
		}
		if len(content) != entry.Size {
			return nil, fmt.Errorf("%w: inconsistent blob", ErrUpstream)
		}
		return content, nil
	}
	var blob struct {
		SHA      string `json:"sha"`
		Size     int    `json:"size"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if err := c.get(ctx, base+"/git/blobs/"+entry.SHA, token, 80_000, budget, &blob); err != nil {
		return nil, err
	}
	if blob.SHA != entry.SHA || blob.Encoding != "base64" || blob.Size != entry.Size {
		return nil, fmt.Errorf("%w: inconsistent blob", ErrUpstream)
	}
	content, err := base64.StdEncoding.DecodeString(blob.Content)
	if err != nil || len(content) != blob.Size {
		return nil, fmt.Errorf("%w: invalid blob", ErrUpstream)
	}
	return content, nil
}
