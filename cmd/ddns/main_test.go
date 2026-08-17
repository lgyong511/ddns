package main

import (
	"path/filepath"
	"testing"
)

func TestResolveConfigPath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		exeDir   string
		expected string
	}{
		{name: "default executable directory", exeDir: "/opt/ddns", expected: "/opt/ddns/config/config.yaml"},
		{name: "explicit absolute path", input: "/etc/ddns/custom.yaml", exeDir: "/opt/ddns", expected: "/etc/ddns/custom.yaml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveConfigPath(tt.input, tt.exeDir)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.expected {
				t.Fatalf("resolveConfigPath() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestResolveConfigPathRelativeExplicitPathUsesWorkingDirectory(t *testing.T) {
	got, err := resolveConfigPath("./config.yaml", "/opt/ddns")
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs("./config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(want) {
		t.Fatalf("resolveConfigPath() = %q, want %q", got, want)
	}
}
