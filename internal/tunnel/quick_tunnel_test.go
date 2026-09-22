package tunnel

import (
	"context"
	"errors"
	"os"
	"testing"
)

// chdirTemp 把工作目录切到一个临时目录，测试结束自动还原——存档读写都是
// 相对 os.Getwd() 的，不这样做会互相污染，也可能真的动到仓库目录。
func chdirTemp(t *testing.T) string {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatal(err)
		}
	})
	return dir
}

func TestTunnelStoreRoundTrip(t *testing.T) {
	chdirTemp(t)

	want := &Info{
		ID:         "tunnel-id",
		Hostname:   "random-words.trycloudflare.com",
		AccountTag: "account-tag",
		Secret:     []byte("super-secret"),
	}
	if err := SaveStore(want); err != nil {
		t.Fatalf("SaveStore: %v", err)
	}

	got, err := loadTunnelStore()
	if err != nil {
		t.Fatalf("loadTunnelStore: %v", err)
	}
	if got.ID != want.ID || got.Hostname != want.Hostname || got.AccountTag != want.AccountTag || string(got.Secret) != string(want.Secret) {
		t.Errorf("round trip mismatch: got %+v, want %+v", got, want)
	}
}

// 存档不存在时，读取应该当成"没有存档"返回 error，不应该 panic 或返回零值成功
func TestLoadTunnelStoreMissing(t *testing.T) {
	chdirTemp(t)

	if _, err := loadTunnelStore(); err == nil {
		t.Error("expected error when store file does not exist")
	}
}

// 存档内容被手工改坏（不是合法 JSON）时，也应该当成"没有存档"处理，不报错打扰用户，
// 只需要 loadTunnelStore 把 error 返回给调用方，由调用方决定转去申请新隧道
func TestLoadTunnelStoreCorrupted(t *testing.T) {
	chdirTemp(t)

	if err := os.WriteFile(StoreFileName, []byte("not valid json{{{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTunnelStore(); err == nil {
		t.Error("expected error for corrupted store file")
	}
}

// 字段缺失（比如手工删掉了 secret）也应该视为不可用的存档
func TestLoadTunnelStoreIncomplete(t *testing.T) {
	chdirTemp(t)

	if err := os.WriteFile(StoreFileName, []byte(`{"id":"x","hostname":"y"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTunnelStore(); err == nil {
		t.Error("expected error when secret is missing")
	}
}

// 有可用存档、且没给 -new 时，AcquireQuick 应该直接用存档，不调 refresh
func TestAcquireQuickTunnelUsesStore(t *testing.T) {
	chdirTemp(t)

	stored := &Info{ID: "stored-id", Hostname: "stored.trycloudflare.com", Secret: []byte("s")}
	if err := SaveStore(stored); err != nil {
		t.Fatal(err)
	}

	called := false
	refresh := func(context.Context) (*Info, error) {
		called = true
		return nil, errors.New("should not be called")
	}

	info, isNew, err := AcquireQuick(context.Background(), false, refresh)
	if err != nil {
		t.Fatalf("AcquireQuick: %v", err)
	}
	if called {
		t.Error("refresh should not be called when a valid store exists")
	}
	if isNew {
		t.Error("isNew should be false when reusing the store")
	}
	if info.ID != stored.ID {
		t.Errorf("got id %q, want %q", info.ID, stored.ID)
	}
}

// -new 应该无视存档，强制走 refresh 申请新隧道
func TestAcquireQuickTunnelForceNewIgnoresStore(t *testing.T) {
	chdirTemp(t)

	stored := &Info{ID: "stored-id", Hostname: "stored.trycloudflare.com", Secret: []byte("s")}
	if err := SaveStore(stored); err != nil {
		t.Fatal(err)
	}

	fresh := &Info{ID: "fresh-id", Hostname: "fresh.trycloudflare.com", Secret: []byte("f")}
	called := false
	refresh := func(context.Context) (*Info, error) {
		called = true
		return fresh, nil
	}

	info, isNew, err := AcquireQuick(context.Background(), true, refresh)
	if err != nil {
		t.Fatalf("AcquireQuick: %v", err)
	}
	if !called {
		t.Error("refresh should be called when forceNew is set")
	}
	if !isNew {
		t.Error("isNew should be true when a new tunnel was requested")
	}
	if info.ID != fresh.ID {
		t.Errorf("got id %q, want %q", info.ID, fresh.ID)
	}
}

// 没有存档时也应该走 refresh，并把 isNew 报成 true
func TestAcquireQuickTunnelNoStoreFallsBackToRefresh(t *testing.T) {
	chdirTemp(t)

	fresh := &Info{ID: "fresh-id", Hostname: "fresh.trycloudflare.com", Secret: []byte("f")}
	refresh := func(context.Context) (*Info, error) {
		return fresh, nil
	}

	info, isNew, err := AcquireQuick(context.Background(), false, refresh)
	if err != nil {
		t.Fatalf("AcquireQuick: %v", err)
	}
	if !isNew {
		t.Error("isNew should be true when there was no store to reuse")
	}
	if info.ID != fresh.ID {
		t.Errorf("got id %q, want %q", info.ID, fresh.ID)
	}
}

// refresh 失败时 AcquireQuick 应该把 error 原样传出去
func TestAcquireQuickTunnelRefreshError(t *testing.T) {
	chdirTemp(t)

	wantErr := errors.New("api unavailable")
	refresh := func(context.Context) (*Info, error) {
		return nil, wantErr
	}

	if _, _, err := AcquireQuick(context.Background(), false, refresh); !errors.Is(err, wantErr) {
		t.Errorf("got error %v, want %v", err, wantErr)
	}
}
