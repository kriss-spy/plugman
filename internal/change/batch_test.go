package change

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestBatchHoldsVaultLockAcrossRecoveryPreflightAndEveryApply(t *testing.T) {
	vault := newVault(t, nil)
	firstStage := filepath.Join(vault, "first")
	secondStage := filepath.Join(vault, "second")
	writePlugin(t, firstStage, "first", "1.0.0", true)
	writePlugin(t, secondStage, "second", "1.0.0", true)

	batch, err := New().BeginBatch(context.Background(), vault)
	if err != nil {
		t.Fatal(err)
	}
	defer batch.Close()
	if _, err := batch.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := PreparedChange{VaultRoot: vault, PluginID: "first", Kind: Install, StagedDir: firstStage}
	second := PreparedChange{VaultRoot: vault, PluginID: "second", Kind: Install, StagedDir: secondStage}
	if err := batch.Validate(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := batch.Validate(context.Background(), second); err != nil {
		t.Fatal(err)
	}

	competing := make(chan error, 1)
	go func() {
		other, beginErr := New().BeginBatch(context.Background(), vault)
		if other != nil {
			other.Close()
		}
		competing <- beginErr
	}()
	if beginErr := <-competing; beginErr == nil || !strings.Contains(beginErr.Error(), "another Plugman mutation") {
		t.Fatalf("competing BeginBatch error = %v", beginErr)
	}

	if _, err := batch.Apply(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if _, err := batch.Apply(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if err := batch.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New().BeginBatch(context.Background(), vault)
	if err != nil {
		t.Fatalf("BeginBatch after Close: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBatchRejectsChangeForAnotherVault(t *testing.T) {
	vault := newVault(t, nil)
	otherVault := newVault(t, nil)
	stage := filepath.Join(otherVault, "stage")
	writePlugin(t, stage, "demo", "1.0.0", true)
	batch, err := New().BeginBatch(context.Background(), vault)
	if err != nil {
		t.Fatal(err)
	}
	defer batch.Close()

	err = batch.Validate(context.Background(), PreparedChange{
		VaultRoot: otherVault, PluginID: "demo", Kind: Install, StagedDir: stage,
	})
	if err == nil || !strings.Contains(err.Error(), "different Vault") {
		t.Fatalf("Validate error = %v", err)
	}
}
