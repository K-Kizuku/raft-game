package stage

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// postmasterTones は口調のキー（データ仕様 §5.4）。web の noticeText.ts と同じ並び。
var postmasterTones = []string{"casual", "polite", "elder"}

// maxPostmasterName は郵便局長の名前の長さの上限（前後の空白を除いた Unicode のコードポイントの数。データ仕様 §5.4）。
const maxPostmasterName = 16

// portraitRe は郵便局長の絵のファイル名（web/public/assets/generated/postmasters/ の下。ファイルがあるかは確かめない）。
var portraitRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*\.(png|webp)$`)

// checkPostmasters は islands[i].postmaster を検査して st.Islands[i].Postmaster に入れる。
// checkIslands の後に呼ぶ（c.islandsOK を変えない）。
func (c *checker) checkPostmasters(st *Stage, list []rawIsland) {
	for i, ri := range list {
		if ri.Postmaster == nil || i >= len(st.Islands) {
			continue
		}
		st.Islands[i].Postmaster = c.postmaster(fmt.Sprintf("islands[%d].postmaster", i), ri.Postmaster)
	}
}

// postmaster は 1 つの島の郵便局長を検査する（データ仕様 §5.4 の表の順）。rp は nil でない。
func (c *checker) postmaster(path string, rp *rawPostmaster) *Postmaster {
	pm := &Postmaster{Name: rp.Name, Tone: rp.Tone, Portrait: rp.Portrait}

	switch n := utf8.RuneCountInString(strings.TrimSpace(rp.Name)); {
	case rp.Name == "":
		c.add(path+".name", msgRequired)
	case n == 0:
		c.add(path+".name", "空白だけにしない")
	case n > maxPostmasterName:
		c.add(path+".name", fmt.Sprintf("%d 文字以下にする（%d 文字）", maxPostmasterName, n))
	}

	switch {
	case rp.Tone == "":
		c.add(path+".tone", msgRequired)
	case !slices.Contains(postmasterTones, rp.Tone):
		c.add(path+".tone", fmt.Sprintf("%s のどれかにする（値: %q）", strings.Join(postmasterTones, "・"), rp.Tone))
	}

	if rp.Portrait != "" && !portraitRe.MatchString(rp.Portrait) {
		c.add(path+".portrait", fmt.Sprintf(`^[a-z0-9][a-z0-9-]*\.(png|webp)$ に合わない（値: %q）`, rp.Portrait))
	}

	switch p := Provenance(rp.Provenance); p {
	case Derived, Creative:
		pm.Provenance = p
	case "":
		c.add(path+".provenance", msgRequired)
	default:
		c.add(path+".provenance", fmt.Sprintf("derived か creative にする（値: %q）", rp.Provenance))
	}
	pm.Source = c.sourceFor(path+".source", pm.Provenance, rp.Source)
	return pm
}
