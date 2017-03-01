package vault

import (
	"encoding/base64"
	"fmt"
	"reflect"
	"testing"

	log "github.com/mgutz/logxi/v1"

	"github.com/hashicorp/vault/helper/logformat"
	"github.com/hashicorp/vault/physical"
)

func TestCore_Rekey_Lifecycle(t *testing.T) {
	bc, rc := TestSealDefConfigs()
	bc.SecretShares = 1
	bc.SecretThreshold = 1
	bc.StoredShares = 0
	c, masterKeys, recoveryKeys, _ := TestCoreUnsealedWithConfigs(t, bc, rc)
	if len(masterKeys) != 1 {
		t.Fatalf("expected %d keys, got %d", bc.SecretShares-bc.StoredShares, len(masterKeys))
	}
	testCore_Rekey_Lifecycle_Common(t, c, masterKeys, false)

	bc, rc = TestSealDefConfigs()
	c, masterKeys, recoveryKeys, _ = TestCoreUnsealedWithConfigs(t, bc, rc)
	if len(masterKeys) != 3 {
		t.Fatalf("expected %d keys, got %d", bc.SecretShares-bc.StoredShares, len(masterKeys))
	}
	testCore_Rekey_Lifecycle_Common(t, c, masterKeys, false)
	testCore_Rekey_Lifecycle_Common(t, c, recoveryKeys, true)
}

func testCore_Rekey_Lifecycle_Common(t *testing.T, c *Core, masterKeys [][]byte, recovery bool) {
	// Verify update not allowed
	if _, err := c.RekeyUpdate(masterKeys[0], "", recovery); err == nil {
		t.Fatalf("no rekey should be in progress")
	}

	// Should be no progress
	num, err := c.RekeyProgress(recovery)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if num != 0 {
		t.Fatalf("bad: %d", num)
	}

	// Should be no config
	conf, err := c.RekeyConfig(recovery)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if conf != nil {
		t.Fatalf("bad: %v", conf)
	}

	// Cancel should be idempotent
	err = c.RekeyCancel(false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	// Start a rekey
	newConf := &SealConfig{
		SecretThreshold: 3,
		SecretShares:    5,
	}
	err = c.RekeyInit(newConf, recovery)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	// Should get config
	conf, err = c.RekeyConfig(recovery)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	newConf.Nonce = conf.Nonce
	if !reflect.DeepEqual(conf, newConf) {
		t.Fatalf("bad: %v", conf)
	}

	// Cancel should be clear
	err = c.RekeyCancel(recovery)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	// Should be no config
	conf, err = c.RekeyConfig(recovery)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if conf != nil {
		t.Fatalf("bad: %v", conf)
	}
}

func TestCore_Rekey_Init(t *testing.T) {
	c, _, _ := TestCoreUnsealed(t)
	testCore_Rekey_Init_Common(t, c, false)

	bc, rc := TestSealDefConfigs()
	c, _, _, _ = TestCoreUnsealedWithConfigs(t, bc, rc)
	testCore_Rekey_Init_Common(t, c, false)
	testCore_Rekey_Init_Common(t, c, true)
}

func testCore_Rekey_Init_Common(t *testing.T, c *Core, recovery bool) {
	// Try an invalid config
	badConf := &SealConfig{
		SecretThreshold: 5,
		SecretShares:    1,
	}
	err := c.RekeyInit(badConf, recovery)
	if err == nil {
		t.Fatalf("should fail")
	}

	// Start a rekey
	newConf := &SealConfig{
		SecretThreshold: 3,
		SecretShares:    5,
	}
	err = c.RekeyInit(newConf, recovery)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	// Second should fail
	err = c.RekeyInit(newConf, recovery)
	if err == nil {
		t.Fatalf("should fail")
	}
}

func TestCore_Rekey_Update(t *testing.T) {
	bc, rc := TestSealDefConfigs()
	bc.SecretShares = 1
	bc.SecretThreshold = 1
	bc.StoredShares = 0
	c, masterKeys, _, root := TestCoreUnsealedWithConfigs(t, bc, rc)
	testCore_Rekey_Update_Common(t, c, masterKeys, root, false, false)

	bc, rc = TestSealDefConfigs()
	bc.StoredShares = 0
	c, masterKeys, recoveryKeys, root := TestCoreUnsealedWithConfigs(t, bc, rc)
	testCore_Rekey_Update_Common(t, c, masterKeys, root, false, false)
	testCore_Rekey_Update_Common(t, c, recoveryKeys, root, true, false)

	bc, rc = TestSealDefConfigs()
	bc.StoredShares = 0
	c, masterKeys, recoveryKeys, root = TestCoreUnsealedWithConfigs(t, bc, rc)
	testCore_Rekey_Update_Common(t, c, masterKeys, root, false, true)
	testCore_Rekey_Update_Common(t, c, recoveryKeys, root, true, true)
}

func testCore_Rekey_Update_Common(t *testing.T, c *Core, keys [][]byte, root string, recovery, wrap bool) {
	// Start a rekey
	var expType string
	if recovery {
		expType = c.seal.RecoveryType()
	} else {
		expType = c.seal.BarrierType()
	}

	newConf := &SealConfig{
		Type:            expType,
		SecretThreshold: 3,
		SecretShares:    5,
		WrapShares:      wrap,
	}

	err := c.RekeyInit(newConf, recovery)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	// Fetch new config with generated nonce
	rkconf, err := c.RekeyConfig(recovery)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if rkconf == nil {
		t.Fatalf("bad: no rekey config received")
	}

	// Provide the master
	var result *RekeyResult
	for _, key := range keys {
		result, err = c.RekeyUpdate(key, rkconf.Nonce, recovery)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if result != nil {
			break
		}
	}
	if result == nil || len(result.SecretShares) != newConf.SecretShares {
		t.Fatalf("Bad: %#v", result)
	}

	// Should be no progress
	num, err := c.RekeyProgress(recovery)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if num != 0 {
		t.Fatalf("bad: %d", num)
	}

	// Should be no config
	conf, err := c.RekeyConfig(recovery)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if conf != nil {
		t.Fatalf("bad: %v", conf)
	}

	// SealConfig should update
	var sealConf *SealConfig
	if recovery {
		sealConf, err = c.seal.RecoveryConfig()
	} else {
		sealConf, err = c.seal.BarrierConfig()
	}
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if sealConf == nil {
		t.Fatal("seal configuration is nil")
	}

	newConf.Nonce = rkconf.Nonce
	if !reflect.DeepEqual(sealConf, newConf) {
		t.Fatalf("\nexpected: %#v\nactual: %#v\nexpType: %s\nrecovery: %t", newConf, sealConf, expType, recovery)
	}

	if wrap {
		for i := 0; i < newConf.SecretShares; i++ {
			result.SecretShares[i], _ = base64.StdEncoding.DecodeString(testValidateWrappedShare(t, c, string(result.SecretShares[i][:]), newConf, "rekey"))
		}
	}

	// Attempt unseal if this was not recovery mode
	if !recovery {
		err = c.Seal(root)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		for i := 0; i < 3; i++ {
			_, err = TestCoreUnseal(c, TestKeyCopy(result.SecretShares[i]))
			if err != nil {
				t.Fatalf("err: %v", err)
			}
		}
		if sealed, _ := c.Sealed(); sealed {
			t.Fatalf("should be unsealed")
		}
	}

	// Start another rekey, this time we require a quorum!
	newConf = &SealConfig{
		Type:            expType,
		SecretThreshold: 1,
		SecretShares:    1,
		WrapShares:      wrap,
	}
	err = c.RekeyInit(newConf, recovery)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	// Fetch new config with generated nonce
	rkconf, err = c.RekeyConfig(recovery)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if rkconf == nil {
		t.Fatalf("bad: no rekey config received")
	}

	// Provide the parts master
	oldResult := result
	for i := 0; i < 3; i++ {
		result, err = c.RekeyUpdate(TestKeyCopy(oldResult.SecretShares[i]), rkconf.Nonce, recovery)
		if err != nil {
			t.Fatalf("err: %v", err)
		}

		// Should be progress
		num, err := c.RekeyProgress(recovery)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if (i == 2 && num != 0) || (i != 2 && num != i+1) {
			t.Fatalf("bad: %d", num)
		}
	}
	if result == nil || len(result.SecretShares) != 1 {
		t.Fatalf("Bad: %#v", result)
	}

	if wrap {
		for i := 0; i < newConf.SecretShares; i++ {
			result.SecretShares[i], _ = base64.StdEncoding.DecodeString(testValidateWrappedShare(t, c, string(result.SecretShares[i][:]), newConf, "rekey"))
		}
	}

	// Attempt unseal if this was not recovery mode
	if !recovery {
		err = c.Seal(root)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		unseal, err := TestCoreUnseal(c, result.SecretShares[0])
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !unseal {
			t.Fatalf("should be unsealed")
		}
	}

	// SealConfig should update
	if recovery {
		sealConf, err = c.seal.RecoveryConfig()
	} else {
		sealConf, err = c.seal.BarrierConfig()
	}
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	newConf.Nonce = rkconf.Nonce
	if !reflect.DeepEqual(sealConf, newConf) {
		t.Fatalf("bad: %#v", sealConf)
	}
}

func TestCore_Rekey_Invalid(t *testing.T) {
	bc, rc := TestSealDefConfigs()
	bc.StoredShares = 0
	bc.SecretShares = 1
	bc.SecretThreshold = 1
	rc.SecretShares = 1
	rc.SecretThreshold = 1
	c, masterKeys, recoveryKeys, _ := TestCoreUnsealedWithConfigs(t, bc, rc)
	testCore_Rekey_Invalid_Common(t, c, masterKeys, false)
	testCore_Rekey_Invalid_Common(t, c, recoveryKeys, true)
}

func testCore_Rekey_Invalid_Common(t *testing.T, c *Core, keys [][]byte, recovery bool) {
	// Start a rekey
	newConf := &SealConfig{
		SecretThreshold: 3,
		SecretShares:    5,
	}
	err := c.RekeyInit(newConf, recovery)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	// Fetch new config with generated nonce
	rkconf, err := c.RekeyConfig(recovery)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if rkconf == nil {
		t.Fatalf("bad: no rekey config received")
	}

	// Provide the nonce (invalid)
	_, err = c.RekeyUpdate(keys[0], "abcd", recovery)
	if err == nil {
		t.Fatalf("expected error")
	}

	// Provide the key (invalid)
	key := keys[0]
	oldkeystr := fmt.Sprintf("%#v", key)
	key[0]++
	newkeystr := fmt.Sprintf("%#v", key)
	ret, err := c.RekeyUpdate(key, rkconf.Nonce, recovery)
	if err == nil {
		t.Fatalf("expected error, ret is %#v\noldkeystr: %s\nnewkeystr: %s", *ret, oldkeystr, newkeystr)
	}
}

func TestCore_Standby_Rekey(t *testing.T) {
	// Create the first core and initialize it
	logger := logformat.NewVaultLogger(log.LevelTrace)

	inm := physical.NewInmem(logger)
	inmha := physical.NewInmemHA(logger)
	redirectOriginal := "http://127.0.0.1:8200"
	core, err := NewCore(&CoreConfig{
		Physical:     inm,
		HAPhysical:   inmha,
		RedirectAddr: redirectOriginal,
		DisableMlock: true,
		DisableCache: true,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	keys, root := TestCoreInit(t, core)
	for _, key := range keys {
		if _, err := TestCoreUnseal(core, TestKeyCopy(key)); err != nil {
			t.Fatalf("unseal err: %s", err)
		}
	}

	// Wait for core to become active
	TestWaitActive(t, core)

	// Create a second core, attached to same in-memory store
	redirectOriginal2 := "http://127.0.0.1:8500"
	core2, err := NewCore(&CoreConfig{
		Physical:     inm,
		HAPhysical:   inmha,
		RedirectAddr: redirectOriginal2,
		DisableMlock: true,
		DisableCache: true,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	for _, key := range keys {
		if _, err := TestCoreUnseal(core2, TestKeyCopy(key)); err != nil {
			t.Fatalf("unseal err: %s", err)
		}
	}

	// Rekey the master key
	newConf := &SealConfig{
		SecretShares:    1,
		SecretThreshold: 1,
	}
	err = core.RekeyInit(newConf, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// Fetch new config with generated nonce
	rkconf, err := core.RekeyConfig(false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if rkconf == nil {
		t.Fatalf("bad: no rekey config received")
	}
	var rekeyResult *RekeyResult
	for _, key := range keys {
		rekeyResult, err = core.RekeyUpdate(key, rkconf.Nonce, false)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
	}
	if rekeyResult == nil {
		t.Fatalf("rekey failed")
	}

	// Seal the first core, should step down
	err = core.Seal(root)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	// Wait for core2 to become active
	TestWaitActive(t, core2)

	// Rekey the master key again
	err = core2.RekeyInit(newConf, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// Fetch new config with generated nonce
	rkconf, err = core2.RekeyConfig(false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if rkconf == nil {
		t.Fatalf("bad: no rekey config received")
	}
	var rekeyResult2 *RekeyResult
	for _, key := range rekeyResult.SecretShares {
		rekeyResult2, err = core2.RekeyUpdate(key, rkconf.Nonce, false)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
	}
	if rekeyResult2 == nil {
		t.Fatalf("rekey failed")
	}

	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if rekeyResult2 == nil {
		t.Fatalf("rekey failed")
	}
}
