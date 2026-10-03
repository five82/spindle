package makemkv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScanAndRipWithStubMakeMKV(t *testing.T) {
	bin := t.TempDir()
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$ARGS_FILE"
case "$3" in
info)
  if [ "$MODE" = fail ]; then exit 2; fi
  printf '%s\n' 'CINFO:2,0,"Fixture Disc"' 'TINFO:1,2,0,"Feature"' 'TINFO:1,9,0,"1:30:00"'
  ;;
mkv)
  if [ "$MODE" = fail ]; then exit 2; fi
  printf '%s\n' 'PRGT:5024,0,"Saving"' 'PRGV:100,100,65536' 'MSG:5036,260,1,"Copy complete. 1 titles saved.","Copy complete. %1 titles saved.","1"'
  if [ "$MODE" != empty ]; then printf 'mkv' > "$6/new.mkv"; fi
  ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "makemkvcon"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	argsFile := filepath.Join(bin, "args")
	t.Setenv("ARGS_FILE", argsFile)
	info, err := Scan(context.Background(), "/dev/sr0", time.Second, 60, nil)
	if err != nil || info.Name != "Fixture Disc" || len(info.Titles) != 1 || info.Titles[0].Duration != 5400 {
		t.Fatalf("scan: %+v %v", info, err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil || !strings.Contains(string(args), "info dev:/dev/sr0 --minlength=60") {
		t.Fatalf("scan args: %s %v", args, err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "old.mkv"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Rip(context.Background(), "disc:0", 1, dir, time.Second, 0, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.mkv")); err != nil {
		t.Fatal(err)
	}
	args, err = os.ReadFile(argsFile)
	if err != nil || !strings.Contains(string(args), "mkv disc:0 1 "+dir+" --minlength=0") {
		t.Fatalf("rip args: %s %v", args, err)
	}
	t.Setenv("MODE", "empty")
	if err := Rip(context.Background(), "disc:0", 1, dir, time.Second, 0, nil, nil); err == nil || !strings.Contains(err.Error(), "no output") {
		t.Fatalf("empty rip: %v", err)
	}
	t.Setenv("MODE", "fail")
	if _, err := Scan(context.Background(), "disc:0", time.Second, 0, nil); err == nil {
		t.Fatal("scan exit ignored")
	}
	if err := Rip(context.Background(), "disc:0", 1, dir, time.Second, 0, nil, nil); err == nil {
		t.Fatal("rip exit ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Scan(ctx, "disc:0", time.Second, 0, nil); err == nil {
		t.Fatal("cancelled scan succeeded")
	}
	if err := Rip(ctx, "disc:0", 1, dir, time.Second, 0, nil, nil); err == nil {
		t.Fatal("cancelled rip succeeded")
	}
}
