package cluster

import (
	"errors"
	"testing"

	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"
)

func validConfig() NodeConfig {
	return NodeConfig{
		ID:              1,
		Peers:           []uint64{1, 2, 3},
		ElectionTick:    16,
		HeartbeatTick:   2,
		MaxSizePerMsg:   4096,
		MaxInflightMsgs: 4,
		RandIntn:        func(int) int { return 0 },
	}
}

func TestNewNodeValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mutate  func(*NodeConfig)
		wantErr bool
	}{
		{name: "valid", mutate: func(*NodeConfig) {}},
		{name: "nil RandIntn", mutate: func(c *NodeConfig) { c.RandIntn = nil }, wantErr: true},
		{name: "empty Peers", mutate: func(c *NodeConfig) { c.Peers = nil }, wantErr: true},
		{name: "duplicate Peers", mutate: func(c *NodeConfig) { c.Peers = []uint64{1, 2, 2} }, wantErr: true},
		{name: "ID not in Peers", mutate: func(c *NodeConfig) { c.ID = 4 }, wantErr: true},
		{name: "ID is zero", mutate: func(c *NodeConfig) { c.ID = 0; c.Peers = []uint64{0, 1, 2} }, wantErr: true},
		{name: "ElectionTick <= HeartbeatTick", mutate: func(c *NodeConfig) { c.ElectionTick = 2 }, wantErr: true},
		{name: "MaxInflightMsgs is zero", mutate: func(c *NodeConfig) { c.MaxInflightMsgs = 0 }, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := validConfig()
			tt.mutate(&cfg)
			n, err := NewNode(cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NewNode: err = nil, want error")
				}
				if n != nil {
					t.Errorf("NewNode: node = %v, want nil on error", n)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewNode: %v", err)
			}
			if n.ID() != cfg.ID {
				t.Errorf("ID() = %d, want %d", n.ID(), cfg.ID)
			}
		})
	}
}

func TestNewNodeDoesNotModifyPeers(t *testing.T) {
	t.Parallel()
	cfg := validConfig()
	cfg.Peers = []uint64{3, 1, 2}
	if _, err := NewNode(cfg); err != nil {
		t.Fatalf("NewNode: %v", err)
	}
	if cfg.Peers[0] != 3 || cfg.Peers[1] != 1 || cfg.Peers[2] != 2 {
		t.Errorf("Peers = %v, want unchanged [3 1 2]", cfg.Peers)
	}
}

func TestDeliverRejectsWrongRecipient(t *testing.T) {
	t.Parallel()
	n, err := NewNode(validConfig())
	if err != nil {
		t.Fatalf("NewNode: %v", err)
	}
	m := &pb.Message{
		Type: pb.MessageType_MsgHeartbeat.Enum(),
		From: proto.Uint64(2),
		To:   proto.Uint64(3),
		Term: proto.Uint64(1),
	}
	if err := n.Deliver(m); err == nil {
		t.Fatalf("Deliver: err = nil, want error for message to node 3")
	}
}

func TestDeliverLocalMessage(t *testing.T) {
	t.Parallel()
	n, err := NewNode(validConfig())
	if err != nil {
		t.Fatalf("NewNode: %v", err)
	}
	m := &pb.Message{
		Type: pb.MessageType_MsgHup.Enum(),
		To:   proto.Uint64(1),
	}
	if err := n.Deliver(m); !errors.Is(err, raft.ErrStepLocalMsg) {
		t.Fatalf("Deliver(MsgHup): err = %v, want ErrStepLocalMsg", err)
	}
}
