package simconv_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/K-Kizuku/raft-game/server/internal/protocol"
)

// simConsts は ../../sim の _test.go 以外のファイルから、文字列の定数を集める。
type simConsts struct {
	eventTypes   []string // 型が EventType
	commandTypes []string // 型が CommandType
	codes        []string // 名前が Code で始まる（型なし）
	phases       []string // 型が Phase
}

func readSimConsts(t *testing.T) simConsts {
	t.Helper()
	dir := filepath.Join("..", "..", "sim")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out simConsts
	fset := token.NewFileSet()
	for _, ent := range ents {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				typ := ""
				if id, ok := vs.Type.(*ast.Ident); ok {
					typ = id.Name
				}
				for i, n := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					v, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatal(err)
					}
					switch {
					case typ == "EventType":
						out.eventTypes = append(out.eventTypes, v)
					case typ == "CommandType":
						out.commandTypes = append(out.commandTypes, v)
					case typ == "Phase":
						out.phases = append(out.phases, v)
					case typ == "" && strings.HasPrefix(n.Name, "Code"):
						out.codes = append(out.codes, v)
					}
				}
			}
		}
	}
	return out
}

// C-1: sim の定数が protocol にそろっている（sim に足したものを protocol に足し忘れると失敗する）。
func TestSimConstantsInProtocol(t *testing.T) {
	t.Parallel()
	c := readSimConsts(t)
	// 読めていることの確認（main と #108 の時点の数）。
	if len(c.eventTypes) < 12 || len(c.commandTypes) < 5 || len(c.codes) < 8 || len(c.phases) < 4 {
		t.Fatalf("too few constants in sim: %+v", c)
	}
	for _, v := range c.eventTypes {
		if !slices.Contains(protocol.EventTypes(), protocol.EventType(v)) {
			t.Errorf("sim EventType %q is not in protocol.EventTypes()", v)
		}
	}
	for _, v := range c.commandTypes {
		if !slices.Contains(protocol.MessageTypes(), protocol.MessageType(v)) {
			t.Errorf("sim CommandType %q is not in protocol.MessageTypes()", v)
		}
	}
	for _, v := range c.codes {
		if !slices.Contains(protocol.AckCodes(), protocol.AckCode(v)) {
			t.Errorf("sim code %q is not in protocol.AckCodes()", v)
		}
	}
	phases := []protocol.Phase{protocol.PhaseIntro, protocol.PhaseOpening, protocol.PhaseSailing, protocol.PhaseEnded}
	for _, v := range c.phases {
		if !slices.Contains(phases, protocol.Phase(v)) {
			t.Errorf("sim Phase %q is not in protocol", v)
		}
	}
}
