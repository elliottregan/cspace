package cli

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestVerifyInContainerResolution(t *testing.T) {
	host := "mercury.p.cspace.test"
	t.Run("resolves and calls getent hosts <host>", func(t *testing.T) {
		var gotArgv []string
		exec := func(_ context.Context, _ string, argv ...string) ([]byte, error) {
			gotArgv = argv
			return []byte("192.168.64.5 " + host + "\n"), nil
		}
		if err := verifyInContainerResolution(context.Background(), exec, "c", host); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
		if want := []string{"getent", "hosts", host}; !reflect.DeepEqual(gotArgv, want) {
			t.Errorf("argv = %v, want %v", gotArgv, want)
		}
	})
	t.Run("empty output fails after retries", func(t *testing.T) {
		exec := func(_ context.Context, _ string, argv ...string) ([]byte, error) { return []byte(""), nil }
		if err := verifyInContainerResolution(context.Background(), exec, "c", host); err == nil {
			t.Fatal("want error on empty resolution")
		}
	})
	t.Run("exec error fails", func(t *testing.T) {
		exec := func(_ context.Context, _ string, argv ...string) ([]byte, error) { return nil, errors.New("boom") }
		if err := verifyInContainerResolution(context.Background(), exec, "c", host); err == nil {
			t.Fatal("want error when exec fails")
		}
	})
}
