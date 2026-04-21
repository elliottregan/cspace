package mcp

import (
	"testing"

	"github.com/elliottregan/cspace/search/config"

	mcpSDK "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestServer_RegistersBothTools(t *testing.T) {
	srv := mcpSDK.NewServer(&mcpSDK.Implementation{Name: "test", Version: "0"}, nil)
	s := &Server{ProjectRoot: ".", Config: &config.Config{
		Corpora: map[string]config.CorpusConfig{
			"code": {Enabled: true},
		},
	}}
	// Register must not panic.
	s.Register(srv)
}
