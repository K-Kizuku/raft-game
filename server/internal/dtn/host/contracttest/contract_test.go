package contracttest_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/dtn/host"
	"github.com/K-Kizuku/raft-game/server/internal/dtn/host/contracttest"
	"github.com/K-Kizuku/raft-game/server/internal/dtn/host/sqlitestore"
)

// TestContract は testdata/contract/*.json の fixture を、Go の SQLite adapter に流す。
func TestContract(t *testing.T) {
	contracttest.RunDir(t, filepath.Join("..", "testdata", "contract"), func(t *testing.T) contracttest.Backend {
		path := filepath.Join(t.TempDir(), "actor.sqlite")
		return contracttest.Backend{
			Open: func() (host.Store, error) { return sqlitestore.Open(path) },
			Destroyed: func() (bool, error) {
				_, err := os.Stat(path)
				if errors.Is(err, os.ErrNotExist) {
					return true, nil
				}
				return false, err
			},
		}
	})
}
