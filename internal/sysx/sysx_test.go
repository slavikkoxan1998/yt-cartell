package sysx

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestHelperProcess is re-executed by TestCommandRuns as a child process.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("SYSX_HELPER") != "1" {
		return
	}
	os.Stdout.WriteString("helper:" + strings.Join(os.Args[len(os.Args)-2:], ","))
	os.Exit(0)
}

func TestCommandRuns(t *testing.T) {
	c := Command(context.Background(), os.Args[0], "-test.run=^TestHelperProcess$", "--", "a b", "ц")
	c.Env = append(os.Environ(), "SYSX_HELPER=1")
	out, err := c.Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := string(out); !strings.Contains(got, "helper:a b,ц") {
		t.Fatalf("unexpected output %q", got)
	}
}

func TestCommandNilContextAndCancel(t *testing.T) {
	var nilCtx context.Context // nil ctx is explicitly supported
	if c := Command(nilCtx, "x"); c == nil {
		t.Fatal("nil cmd")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := Command(ctx, os.Args[0], "-test.run=^TestHelperProcess$")
	c.Env = append(os.Environ(), "SYSX_HELPER=1")
	if err := c.Run(); err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestExeDir(t *testing.T) {
	d := ExeDir()
	if d == "" {
		t.Fatal("empty ExeDir")
	}
	if st, err := os.Stat(d); err != nil || !st.IsDir() {
		t.Fatalf("ExeDir %q is not a directory: %v", d, err)
	}
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	if !strings.EqualFold(filepath.Clean(d), filepath.Dir(exe)) {
		t.Fatalf("ExeDir %q, want %q", d, filepath.Dir(exe))
	}
}

func TestOpenRevealValidate(t *testing.T) {
	if err := OpenPath(""); err == nil {
		t.Fatal("empty path must fail")
	}
	missing := filepath.Join(t.TempDir(), "nope", "missing.mp4")
	if err := OpenPath(missing); err == nil || !strings.Contains(err.Error(), "не знайдено") {
		t.Fatalf("missing file: %v", err)
	}
	if err := RevealPath(missing); err == nil {
		t.Fatal("reveal of missing file in missing dir must fail")
	}
}

func TestCleanExts(t *testing.T) {
	got := cleanExts([]string{".MP3", "*.wav", "mp3", "", "  ogg ", "a b", "../x", "m4a"})
	want := []string{"mp3", "wav", "ogg", "m4a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cleanExts = %v, want %v", got, want)
	}
	if extOf("final.MP4") != "mp4" || extOf("noext") != "" {
		t.Fatal("extOf")
	}
}

func TestPSQuote(t *testing.T) {
	cases := map[string]string{
		"":              "''",
		"plain":         "'plain'",
		"it's":          "'it''s'",
		"пам’ять":       "'пам’’ять'",
		"‘a’ ‚b‛":       "'‘‘a’’ ‚‚b‛‛'",
		`C:\x "y" $env`: `'C:\x "y" $env'`,
		"nul\x00byte":   "'nulbyte'",
	}
	for in, want := range cases {
		if got := psQuote(in); got != want {
			t.Errorf("psQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWinFilters(t *testing.T) {
	if got := winFilter(nil); got != "Усі файли (*.*)|*.*" {
		t.Fatalf("winFilter(nil) = %q", got)
	}
	want := "Підтримувані файли (*.mp3, *.wav)|*.mp3;*.wav|Усі файли (*.*)|*.*"
	if got := winFilter([]string{"mp3", "wav"}); got != want {
		t.Fatalf("winFilter = %q", got)
	}
	if got := winSaveFilter("mp4"); got != "MP4 (*.mp4)|*.mp4|Усі файли (*.*)|*.*" {
		t.Fatalf("winSaveFilter = %q", got)
	}
}

func TestPSScripts(t *testing.T) {
	s := psFolderScript("Вибери папку", `C:\Дані`)
	if a := psArgs(s); a[len(a)-1] != s || !strings.Contains(strings.Join(a, " "), "-NoProfile") || !strings.Contains(strings.Join(a, " "), "-STA") {
		t.Errorf("psArgs = %v", a[:len(a)-1])
	}
	for _, part := range []string{"FolderBrowserDialog", "$d.Description = 'Вибери папку'", `$d.SelectedPath = 'C:\Дані'`, "ToBase64String", "TopMost"} {
		if !strings.Contains(s, part) {
			t.Errorf("folder script lacks %q", part)
		}
	}
	if strings.Contains(psFolderScript("t", ""), "SelectedPath =") {
		t.Error("empty start must not set SelectedPath")
	}
	if strings.ContainsAny(s, "\"\n") {
		t.Error("script must be one line without double quotes")
	}
	f := psFileScript("Музика", []string{"mp3"})
	if !strings.Contains(f, "OpenFileDialog") || !strings.Contains(f, "*.mp3") {
		t.Errorf("file script: %s", f)
	}
	sv := psSaveScript("Зберегти", `C:\Out`, "final.mp4", "mp4")
	for _, part := range []string{"SaveFileDialog", "$d.FileName = 'final.mp4'", "$d.DefaultExt = 'mp4'", `$d.InitialDirectory = 'C:\Out'`, "OverwritePrompt"} {
		if !strings.Contains(sv, part) {
			t.Errorf("save script lacks %q", part)
		}
	}
}

func TestPSResult(t *testing.T) {
	path := `C:\Users\Тарас\Відео\фінал.mp4`
	enc := base64.StdEncoding.EncodeToString([]byte(path))
	if got := psResult([]byte("\ufeff" + enc + "\r\n")); got != path {
		t.Fatalf("psResult = %q", got)
	}
	if psResult([]byte("  \r\n")) != "" {
		t.Fatal("blank output = cancelled")
	}
}

func TestOSAArgs(t *testing.T) {
	a := osaFolderArgs(`Папка "x"`, "/Users/t/Movies")
	if a[len(a)-2] != `Папка "x"` || a[len(a)-1] != "/Users/t/Movies" {
		t.Fatalf("argv not passed through: %v", a)
	}
	joined := strings.Join(a, "\n")
	if !strings.Contains(joined, "default location") || !strings.Contains(joined, "on run argv") {
		t.Fatalf("folder script: %v", a)
	}
	if strings.Contains(strings.Join(osaFolderArgs("t", ""), "\n"), "default location") {
		t.Fatal("no start → no default location")
	}
	f := osaFileArgs("t", []string{"mp3", "wav"})
	if f[len(f)-3] != "t" || f[len(f)-2] != "mp3" || f[len(f)-1] != "wav" || !strings.Contains(strings.Join(f, "\n"), "of type exts") {
		t.Fatalf("file args: %v", f)
	}
	s := osaSaveArgs("t", "/tmp", "a.mp4")
	if s[len(s)-2] != "a.mp4" || s[len(s)-1] != "/tmp" {
		t.Fatalf("save args: %v", s)
	}
	if !osaCancelled([]byte("execution error: User canceled. (-128)")) || osaCancelled([]byte("other")) {
		t.Fatal("osaCancelled")
	}
}

func TestLinuxArgs(t *testing.T) {
	z := zenityFolderArgs("Папка", "/home/t/")
	if !reflect.DeepEqual(z, []string{"--file-selection", "--directory", "--title=Папка", "--filename=/home/t/"}) {
		t.Fatalf("zenity folder: %v", z)
	}
	zf := zenityFileArgs("t", []string{"mp3"})
	if zf[2] != "--file-filter=Підтримувані файли | *.mp3 *.MP3" || zf[3] != "--file-filter=Усі файли | *" {
		t.Fatalf("zenity file: %v", zf)
	}
	zs := zenitySaveArgs("t", "/out", "a.mp4", "mp4")
	if !strings.Contains(strings.Join(zs, " "), "--filename=/out/a.mp4") || !strings.Contains(strings.Join(zs, " "), "--save") {
		t.Fatalf("zenity save: %v", zs)
	}
	k := kdialogFileArgs("t", []string{"wav"}, "/home/t")
	if !reflect.DeepEqual(k, []string{"--title", "t", "--getopenfilename", "/home/t", "*.wav *.WAV|Підтримувані файли"}) {
		t.Fatalf("kdialog file: %v", k)
	}
	if kf := kdialogFolderArgs("t", "", "/home/t"); kf[3] != "/home/t" {
		t.Fatalf("kdialog folder: %v", kf)
	}
	if ks := kdialogSaveArgs("t", "", "a.mp4", "mp4", "/home/t"); ks[3] != "/home/t/a.mp4" || ks[4] != "*.mp4 *.MP4|Підтримувані файли" {
		t.Fatalf("kdialog save: %v", ks)
	}
}

func TestDialogOutput(t *testing.T) {
	if got := dialogOutput([]byte("/home/t/a b.mp3\n")); got != "/home/t/a b.mp3" {
		t.Fatalf("dialogOutput = %q", got)
	}
}

func TestTailRuneBoundary(t *testing.T) {
	got := tail([]byte("помилка: доступ заборонено"), 7)
	if !strings.HasPrefix(got, "…") || !utf8.ValidString(got) || !strings.HasSuffix(got, "но") {
		t.Fatalf("tail = %q", got)
	}
	if tail([]byte(" short "), 50) != "short" {
		t.Fatal("tail trims")
	}
}
