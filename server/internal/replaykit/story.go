package replaykit

import (
	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/stage"
)

// StoryNames は文章に使う島（stage.Islands の順 = ノード ID の順）と資源（ID → Label）の名前。
func StoryNames(st *stage.Stage) game.StoryNames {
	n := game.StoryNames{
		Islands:   make([]string, len(st.Islands)),
		Resources: make(map[string]string, len(st.Resources)),
	}
	for i, is := range st.Islands {
		n.Islands[i] = is.Name
	}
	for _, r := range st.Resources {
		n.Resources[r.ID] = r.Label
	}
	return n
}
