package stage

import "testing"

// SP-2 採点・満足度の新しい項目の検査。
func TestParseScoreValidation(t *testing.T) {
	t.Parallel()
	is := fixtureIslands(t)
	alpha := readTestdata(t, "stages/alpha.yaml")
	const pointsLine = "  points: { normal: 100, critical: 300, late: 30, rejected: 0, expiredPenalty: 50 }\n"
	cases := []struct {
		name  string
		edits []edit
		paths []string // すべて出ること。"" は yaml の読み込みのエラー
	}{
		{"pointsなし", []edit{{pointsLine, ""}}, []string{"scoring.points"}},
		{"lateなし", []edit{{", late: 30", ""}}, []string{"scoring.points.late"}},
		{"expiredPenalty負", []edit{{"expiredPenalty: 50", "expiredPenalty: -1"}}, []string{"scoring.points.expiredPenalty"}},
		{"normal小数", []edit{{"normal: 100", "normal: 1.5"}}, []string{""}},
		{"ferryCostMsPerPoint=0", []edit{{"ferryCostMsPerPoint: 2000", "ferryCostMsPerPoint: 0"}}, []string{"scoring.ferryCostMsPerPoint"}},
		{"fairnessPerSatisfactionなし", []edit{{"  fairnessPerSatisfaction: 5\n", ""}}, []string{"scoring.fairnessPerSatisfaction"}},
		{"succeeded=101", []edit{{"succeeded: 10", "succeeded: 101"}}, []string{"satisfaction.succeeded"}},
		{"rejectedPenaltyなし", []edit{{"  rejectedPenalty: 3\n", ""}}, []string{"satisfaction.rejectedPenalty"}},
		{"satisfaction.expiredPenalty負", []edit{{"expiredPenalty: 10", "expiredPenalty: -1"}}, []string{"satisfaction.expiredPenalty"}},
		{"targetなしでも兄弟を検査", []edit{
			{"target: { successes: 2, score3: 500 }", "target: ~"},
			{"ferryCostMsPerPoint: 2000", "ferryCostMsPerPoint: 0"},
		}, []string{"scoring.target", "scoring.ferryCostMsPerPoint"}},
		{"silencePenaltyなしでも続きを検査", []edit{
			{"  silencePenalty: { everyMs: 10000, amount: 2 }\n", ""},
			{"succeeded: 10", "succeeded: 101"},
		}, []string{"satisfaction.silencePenalty", "satisfaction.succeeded"}},
		{"pointsに未知のキー", []edit{{"expiredPenalty: 50 }", "expiredPenalty: 50, bonus: 1 }"}}, []string{""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st, err := Parse("stages/x.yaml", []byte(applyEdits(t, alpha, tc.edits...)), is)
			if st != nil {
				t.Fatal("エラーなのに *Stage を返した")
			}
			for _, p := range tc.paths {
				requireIssue(t, err, "stages/x.yaml", p)
			}
			se := asError(t, err)
			if len(se.Issues) != len(tc.paths) {
				t.Errorf("Issues = %+v, want paths %v", se.Issues, tc.paths)
			}
		})
	}
}
