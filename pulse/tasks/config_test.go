package tasks

import (
	"strings"
	"testing"
	"time"

	"github.com/gkgoat1/scripts/commitment"
)

func TestParseEveryDomainAndDomainSeparatedLeaves(t *testing.T) {
	const src = `task: scheduled
 domain: scheduled
 interval: 1m
 command: echo same

 task: user
 domain: user
 command: echo same

 task: service
 domain: service
 command: echo same
 restart: on-failure
 restart-min-delay: 1s
 restart-max-delay: 2s
 restart-max-attempts: 2

 task: rapid
 domain: rapid-service
 command: echo same
 restart: always
 restart-min-delay: 0s

 task: stoppable
 domain: stoppable-service
 command: echo same
 restart: on-failure
 restart-min-delay: 1s
 restart-max-delay: 2s
 restart-max-attempts: 2
 pause-max-duration: 1m

 task: disabled
 domain: disabled
 command: echo same
 reason: retained
`
	ts, err := ParseConfig(strings.NewReader(src))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(ts) != 6 {
		t.Fatalf("got %d tasks, want 6", len(ts))
	}
	keys := map[string]bool{}
	for _, task := range ts {
		leaf := task.CommitLeaf()
		if keys[leaf.Key()] {
			t.Errorf("duplicate key %q", leaf.Key())
		}
		keys[leaf.Key()] = true
	}
	if ts[3].RestartMinDelay != 0 || ts[3].Domain != RapidService {
		t.Errorf("rapid task = %+v", ts[3])
	}
}

func TestParseRejectsIllegalRapidPolicy(t *testing.T) {
	cases := []string{
		"task: x\ndomain: rapid-service\ncommand: true\nrestart: on-failure\nrestart-min-delay: 0s\n",
		"task: x\ndomain: rapid-service\ncommand: true\nrestart: always\nrestart-min-delay: 1s\n",
		"task: x\ndomain: rapid-service\ncommand: true\nrestart: always\nrestart-min-delay: 0s\nrestart-max-attempts: 1\n",
	}
	for _, src := range cases {
		if _, err := ParseConfig(strings.NewReader(src)); err == nil {
			t.Errorf("ParseConfig(%q): want error", src)
		}
	}
}

func TestCommitLeafBindsScheduledPolicy(t *testing.T) {
	max := 1.0
	a := Task{ID: "x", Domain: Scheduled, Command: "true", Interval: time.Minute, MaxLoad1: &max}.CommitLeaf()
	b := Task{ID: "x", Domain: Scheduled, Command: "true", Interval: time.Second, MaxLoad1: &max}.CommitLeaf()
	tree, err := commitment.Build([]commitment.Leaf{a})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := tree.ProofFor(a.Key())
	if err != nil {
		t.Fatal(err)
	}
	if commitment.VerifyProof(b, proof, tree.Root()) {
		t.Error("changed interval verified")
	}
}
