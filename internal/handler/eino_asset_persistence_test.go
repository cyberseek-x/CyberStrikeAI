package handler

import "testing"

func TestShouldRunAssetPersistenceOnce(t *testing.T) {
	state := assetPersistenceState{DiscoveryRequest: true, HasDiscoveryEvidence: true}
	if !state.ShouldRun() {
		t.Fatal("expected persistence")
	}
	state.Attempted = true
	if state.ShouldRun() {
		t.Fatal("must not run twice")
	}
}

func TestAssetPersistenceDoesNotRunAfterSuccessfulWrite(t *testing.T) {
	state := assetPersistenceState{DiscoveryRequest: true, HasDiscoveryEvidence: true, HasSuccessfulAssetWrite: true}
	if state.ShouldRun() {
		t.Fatal("successful asset write must not trigger compensation")
	}
}
