package stage

import "fmt"

// rule は、イベントの種類ごとのキーの扱い（データ仕様 §5.4 のイベントの表）。
type rule int

const (
	forbidden rule = iota // 書かない
	required              // 必須
	optional              // 任意
)

// eventSpec は 1 つの種類のイベントで、各キーをどう扱うか。
type eventSpec struct {
	when, duration, routes, forecast, flavor      rule
	island, probability, slot, provenance, source rule
	ferry, requests, letters, minAge              rule
}

// specFor は種類ごとの eventSpec を返す（map を使わず、switch で決める）。
func specFor(k EventKind) (eventSpec, bool) {
	switch k {
	case EventStorm:
		return eventSpec{when: required, duration: required, routes: required, forecast: required, flavor: optional}, true
	case EventCancellation:
		return eventSpec{duration: required, island: required, probability: required, slot: required, provenance: required, source: optional}, true
	case EventBreakdown:
		return eventSpec{when: required, duration: required, ferry: required}, true
	case EventRush:
		return eventSpec{when: required, forecast: required, requests: required}, true
	case EventDrift:
		return eventSpec{when: required, letters: required, minAge: required}, true
	}
	return eventSpec{}, false
}

// forbid は、その種類で書かないキーを書いていたら Issue にする。
func (c *checker) forbid(path string, set bool, kind EventKind) {
	if set {
		c.add(path, fmt.Sprintf("%s では書かない", kind))
	}
}

// event は 1 つのイベントを、イベントの表の行の順に検査する。
func (c *checker) event(path string, re *rawEvent) Event {
	ev := Event{Kind: EventKind(re.Kind)}
	sp, ok := specFor(ev.Kind)
	if !ok {
		if re.Kind == "" {
			c.add(path+".kind", msgRequired)
		} else {
			c.add(path+".kind", fmt.Sprintf("storm・cancellation・breakdown・rush・drift のどれかにする（値: %q）", re.Kind))
		}
		return ev
	}
	k := ev.Kind

	// at か window
	var start int64
	startOK := false
	if sp.when == forbidden {
		c.forbid(path+".at", re.At.set, k)
		c.forbid(path+".window", re.Window != nil, k)
	} else {
		switch {
		case re.At.set && re.Window != nil:
			c.add(path+".window", "at と window の一方だけを書く")
		case re.At.set:
			if v, ok := c.reqInt(path+".at", re.At, 0, c.durationMax); ok {
				ev.When = Window{Start: v, End: v}
				start, startOK = v, true
			}
		case re.Window != nil:
			if w, ok := c.window(path+".window", *re.Window); ok {
				ev.When = w
				start, startOK = w.Start, true
			}
		default:
			c.add(path+".at", "at か window のどちらかを書く")
		}
	}

	// durationMs
	if sp.duration == required {
		ev.DurationMs, _ = c.reqInt(path+".durationMs", re.DurationMs, 1, maxInt)
	} else {
		c.forbid(path+".durationMs", re.DurationMs.set, k)
	}

	// routes
	if sp.routes == required {
		if re.Routes == nil {
			c.add(path+".routes", msgRequired)
		} else {
			ev.Routes = c.stormRoutes(path+".routes", *re.Routes)
		}
	} else {
		c.forbid(path+".routes", re.Routes != nil, k)
	}

	// forecastMs（開始の時刻以下）
	if sp.forecast == required {
		hi := int64(maxInt)
		if startOK {
			hi = start
		}
		if !re.ForecastMs.set {
			c.add(path+".forecastMs", msgRequired)
		} else if v := re.ForecastMs.value; v < 0 || v > hi {
			c.add(path+".forecastMs", fmt.Sprintf("0 以上で、開始の時刻 %d 以下にする（値: %d）", hi, v))
		} else {
			ev.ForecastMs = v
		}
	} else {
		c.forbid(path+".forecastMs", re.ForecastMs.set, k)
	}

	// flavor
	if sp.flavor == optional {
		if re.Flavor != nil {
			t := c.text(path+".flavor", re.Flavor)
			ev.Flavor = &t
		}
	} else {
		c.forbid(path+".flavor", re.Flavor != nil, k)
	}

	// island
	if sp.island == required {
		if re.Island == nil {
			c.add(path+".island", msgRequired)
		} else {
			ev.Island, _ = c.island(path+".island", *re.Island)
		}
	} else {
		c.forbid(path+".island", re.Island != nil, k)
	}

	// probabilityPermille
	if sp.probability == required {
		ev.ProbabilityPermille, _ = c.reqInt(path+".probabilityPermille", re.ProbabilityPermille, 0, permille)
	} else {
		c.forbid(path+".probabilityPermille", re.ProbabilityPermille.set, k)
	}

	// slotMs
	if sp.slot == required {
		ev.SlotMs, _ = c.reqInt(path+".slotMs", re.SlotMs, 1, maxInt)
	} else {
		c.forbid(path+".slotMs", re.SlotMs.set, k)
	}

	// provenance
	if sp.provenance == required {
		if re.Provenance == nil {
			c.add(path+".provenance", msgRequired)
		} else {
			ev.Provenance, _ = c.provenance(path+".provenance", *re.Provenance)
		}
	} else {
		c.forbid(path+".provenance", re.Provenance != nil, k)
	}

	// source
	if sp.source == optional {
		ev.Source = c.sourceFor(path+".source", ev.Provenance, re.Source)
	} else {
		c.forbid(path+".source", re.Source != nil, k)
	}

	// ferry
	if sp.ferry == required {
		switch {
		case re.Ferry == nil || *re.Ferry == "":
			c.add(path+".ferry", msgRequired)
		case *re.Ferry != anyFerry && !c.ferries[*re.Ferry]:
			c.add(path+".ferry", fmt.Sprintf("ferries の id か any にする（値: %q）", *re.Ferry))
		default:
			ev.Ferry = *re.Ferry
		}
	} else {
		c.forbid(path+".ferry", re.Ferry != nil, k)
	}

	// requests
	if sp.requests == required {
		v, _ := c.reqInt(path+".requests", re.Requests, 1, maxInt)
		ev.Requests = int(v)
	} else {
		c.forbid(path+".requests", re.Requests.set, k)
	}

	// letters
	if sp.letters == required {
		v, _ := c.reqInt(path+".letters", re.Letters, 1, maxInt)
		ev.Letters = int(v)
	} else {
		c.forbid(path+".letters", re.Letters.set, k)
	}

	// minAgeMs
	if sp.minAge == required {
		ev.MinAgeMs, _ = c.reqInt(path+".minAgeMs", re.MinAgeMs, 0, maxInt)
	} else {
		c.forbid(path+".minAgeMs", re.MinAgeMs.set, k)
	}
	return ev
}

// window は [start, end] を検査する（start ≤ end ≤ durationMs）。
func (c *checker) window(path string, w []yInt) (Window, bool) {
	if len(w) != 2 || !w[0].set || !w[1].set {
		c.add(path, "[start, end] の 2 つの整数にする")
		return Window{}, false
	}
	s, e := w[0].value, w[1].value
	if s < 0 || s > e || e > c.durationMax {
		c.add(path, fmt.Sprintf("0 ≤ start ≤ end ≤ %d にする（値: [%d, %d]）", c.durationMax, s, e))
		return Window{}, false
	}
	return Window{Start: s, End: e}, true
}

// stormRoutes は嵐の対象の航路（2 つの ref の組の列）を検査して、ノード ID の組にする。
func (c *checker) stormRoutes(path string, list [][]string) [][2]uint64 {
	if len(list) == 0 {
		c.add(path, "1 個以上にする")
		return nil
	}
	var out [][2]uint64
	seen := map[[2]uint64]bool{}
	for j, pr := range list {
		p := fmt.Sprintf("%s[%d]", path, j)
		if len(pr) != 2 {
			c.add(p, "2 つの島の ref の組にする")
			continue
		}
		a, okA := c.refs[pr[0]]
		b, okB := c.refs[pr[1]]
		if !okA || !okB {
			// 誤りのある島を指すときは、Issue を増やさない。
			knownA := okA || c.badRefs[pr[0]]
			knownB := okB || c.badRefs[pr[1]]
			if !knownA || !knownB {
				c.add(p, fmt.Sprintf("islands の ref にない島がある（値: %q）", pr))
			}
			continue
		}
		key := pairKey(a, b)
		switch {
		case a == b || !c.routes[key]:
			// routes に誤りがあるときは、その航路を指しているかもしれないので Issue を増やさない。
			if a == b || c.routesOK {
				c.add(p, fmt.Sprintf("routes にない航路（値: %q）", pr))
			}
		case seen[key]:
			c.add(p, "同じ航路を 2 回書いている")
		default:
			seen[key] = true
			out = append(out, [2]uint64{a, b})
		}
	}
	return out
}

// demo はデモ用の設定を検査する。
func (c *checker) demo(d *rawDemo) *Demo {
	out := &Demo{EndOnAllCriticalSecured: c.reqBool("demo.endOnAllCriticalSecured", d.EndOnAllCriticalSecured)}
	if d.Storm == nil {
		return out
	}
	s := &DemoStorm{}
	if d.Storm.Routes == nil {
		c.add("demo.storm.routes", msgRequired)
	} else {
		s.Routes = c.stormRoutes("demo.storm.routes", *d.Storm.Routes)
	}
	s.DurationMs, _ = c.reqInt("demo.storm.durationMs", d.Storm.DurationMs, 1, maxInt)
	if d.Storm.Flavor != nil {
		t := c.text("demo.storm.flavor", d.Storm.Flavor)
		s.Flavor = &t
	}
	out.Storm = s
	return out
}
