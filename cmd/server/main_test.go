package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestChatGPTRequiresExplicitLocalConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, enabled, addr   string
		wantError, wantClient bool
	}{
		{"disabled remote API", "false", "0.0.0.0:8080", false, false},
		{"unset", "", "127.0.0.1:8080", false, false},
		{"invalid switch", "yes", "127.0.0.1:8080", true, false},
		{"remote listener", "true", "0.0.0.0:8080", true, false},
		{"hostname listener", "true", "localhost:8080", true, false},
		{"local listener", "true", "127.0.0.1:8080", false, true},
		{"IPv6 loopback", "true", "[::1]:8080", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "credentials")
			t.Setenv("QA_CHATGPT_ENABLED", tc.enabled)
			t.Setenv("QA_CHATGPT_STORAGE_DIR", dir)
			client, err := configureChatGPT(tc.addr)
			if (err != nil) != tc.wantError || (client != nil) != tc.wantClient {
				t.Fatalf("client exists %v error %v", client != nil, err)
			}
			if client != nil {
				if err := client.Close(); err != nil {
					t.Fatal(err)
				}
			} else if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("disabled or rejected configuration wrote credentials")
			}
		})
	}
}

func TestOpenCodeRequiresExplicitLocalConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, enabled, addr, binary string
		wantError                   bool
	}{
		{"unset", "", "0.0.0.0:8080", "", false},
		{"disabled", "false", "0.0.0.0:8080", "", false},
		{"invalid switch", "yes", "127.0.0.1:8080", "", true},
		{"remote listener", "true", "0.0.0.0:8080", "", true},
		{"hostname listener", "true", "localhost:8080", "", true},
		{"missing binary", "true", "127.0.0.1:8080", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "opencode")
			t.Setenv("QA_OPENCODE_ENABLED", tc.enabled)
			t.Setenv("QA_OPENCODE_BINARY", tc.binary)
			t.Setenv("QA_OPENCODE_STORAGE_DIR", directory)
			client, err := configureOpenCode(tc.addr)
			if (err != nil) != tc.wantError || client != nil {
				t.Fatalf("client %v err %v", client, err)
			}
			if _, err := os.Stat(directory); !os.IsNotExist(err) {
				t.Fatal("rejected configuration created runtime storage")
			}
		})
	}
}
