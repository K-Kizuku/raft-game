package islandcore

import (
	"errors"
	"fmt"

	"go.etcd.io/raft/v3"

	"github.com/K-Kizuku/raft-game/server/internal/ferry"
	"github.com/K-Kizuku/raft-game/server/internal/game"
	"github.com/K-Kizuku/raft-game/server/internal/ledger"
)

// 依頼の窓口（DesignDoc §8.1）。窓口が読むのは自島のノードの役割と知っている Leader だけで、Leader を決めない。

// route は、この島の窓口が依頼 i を代表の島へ届ける。hops は、この島で依頼書を書くときの hops。
// 判断を request.routed で出す（Coordinator が依頼の状態の材料にする）。
func (c *Core) route(i int, hops int) {
	r := &c.reqs[i]
	if r.resolved {
		return
	}
	st := c.node.Status()
	d := game.Decide(st.Role == raft.StateLeader, st.Lead, c.id)
	switch d.Action {
	case game.ActPropose:
		data, err := ledger.Encode(r.req.Command())
		if err != nil {
			panic(fmt.Sprintf("islandcore: request %s: encode: %v", r.req.ID, err))
		}
		err = c.node.Propose(data)
		switch {
		case err == nil:
			c.emit(OutRequestRouted, RequestRoutedOut{Req: i, ReqID: r.req.ID, Action: ActionProposed})
			if c.id == r.req.Origin {
				c.armRetry(i)
			}
		case errors.Is(err, raft.ErrProposalDropped):
			c.waitRoute(i, hops)
		default:
			panic(fmt.Sprintf("islandcore: request %s: propose at %d: %v", r.req.ID, c.id, err))
		}
	case game.ActLetter:
		l, dropped, err := c.port.PostRequest(c.now, d.To, ferry.RequestNote{ReqID: r.req.ID, Hops: hops})
		if err != nil {
			panic(fmt.Sprintf("islandcore: request %s: post at %d: %v", r.req.ID, c.id, err))
		}
		c.emit(OutLetterCreated, LetterCreatedOut{Letter: l, SenderTerm: st.Term})
		c.emitDrops(dropped)
		c.emit(OutRequestRouted, RequestRoutedOut{Req: i, ReqID: r.req.ID, Action: ActionForwarded, To: d.To, LetterID: l.ID})
		if c.id == r.req.Origin {
			c.armRetry(i)
		}
	case game.ActWait:
		c.waitRoute(i, hops)
	default:
		panic(fmt.Sprintf("islandcore: unknown route action %d", d.Action))
	}
}

// waitRoute は、代表が分からないので待つ。同じ依頼の待ちは 1 つだけ。再送のタイマーは積まない。
func (c *Core) waitRoute(i int, hops int) {
	c.emit(OutRequestRouted, RequestRoutedOut{Req: i, ReqID: c.reqs[i].req.ID, Action: ActionWaiting})
	for _, w := range c.waits {
		if w.Req == i {
			return
		}
	}
	c.waits = append(c.waits, Wait{Req: i, Hops: hops, Seq: c.nextSeq()})
}

// armRetry は、依頼元が送ったので、再送のタイマーを積み直す（古いタイマーは世代で無効にする）。
// ラウンドの長さより後でも積む（ラウンドが先に終わるので処理されない）。
func (c *Core) armRetry(i int) {
	r := &c.reqs[i]
	r.gen++
	c.push(c.now+c.retryMs, ClassRequestRetry, KindRequestRetry, i, r.gen)
}

// deliverRequest は依頼書 l が届いたとき、この島の窓口に渡す。
// 計画にない reqId（テストが直接作った依頼書など）・まだ発生していない依頼は何もしない。
func (c *Core) deliverRequest(l ferry.Letter) {
	i, ok := c.byID[l.Req.ReqID]
	if !ok || !c.reqs[i].created {
		return
	}
	c.route(i, l.Req.Hops+1)
}

// observeApplied は、自島の台帳への適用の結果 r を見る。自島が依頼元で、重複でなければ、結果が出たとする。
func (c *Core) observeApplied(r ledger.Result) {
	i, ok := c.byID[r.ReqID]
	if !ok || r.Status == ledger.StatusDuplicate {
		return
	}
	if c.reqs[i].req.Origin == c.id {
		c.reqs[i].resolved = true
	}
}

// MarkCreated は、依頼 reqID が発生したことを知らせる（依頼元でない島。参照モデルは発生の時点で全島に呼ぶ）。
// 計画にない reqID は何もしない。
func (c *Core) MarkCreated(reqID string) {
	if i, ok := c.byID[reqID]; ok {
		c.reqs[i].created = true
	}
}

// MarkResolved は、依頼 reqID の依頼元の台帳の結果が出たことを知らせる（依頼元でない島。参照モデルは結果が出た
// 時点で全島に呼ぶ）。以後、この島の窓口はその依頼を route しない。計画にない reqID は何もしない。
func (c *Core) MarkResolved(reqID string) {
	if i, ok := c.byID[reqID]; ok {
		c.reqs[i].resolved = true
	}
}

// RetryGen は依頼 i の再送のタイマーの世代を返す（テスト・観察用）。
func (c *Core) RetryGen(i int) uint64 { return c.reqs[i].gen }

// Route は、時刻 t にこの島の窓口に依頼 i を route させる（テストが使う。hops はこの島で依頼書を書くときの hops）。
func (c *Core) Route(t int64, i, hops int) {
	c.begin(t, ClassCommand)
	c.route(i, hops)
}

// AddWait は、依頼 i をこの島で待たせる（テストが使う。hops はこの島で依頼書を書くときの hops）。
func (c *Core) AddWait(i, hops int) {
	c.waits = append(c.waits, Wait{Req: i, Hops: hops, Seq: c.nextSeq()})
}
