package sysx

import (
	"encoding/base64"
	"strings"
)

// Builders for the per-OS dialog helpers. They are plain functions (no build
// tags) so every platform's quoting is unit-tested on any machine.

const (
	labelSupported = "Підтримувані файли"
	labelAll       = "Усі файли"
)

// ---- Windows: PowerShell + WinForms ----------------------------------------------

// psQuote renders s as a PowerShell single-quoted literal. PowerShell also
// treats the typographic quotes ‘ ’ ‚ ‛ as single quotes (common in Ukrainian
// text, e.g. "пам’ять"), so those are doubled too.
func psQuote(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		switch r {
		case 0:
			continue
		case '\'', '‘', '’', '‚', '‛':
			b.WriteRune(r)
		}
		b.WriteRune(r)
	}
	b.WriteByte('\'')
	return b.String()
}

// psPrelude loads WinForms and creates a hidden topmost owner form, so the
// dialog opens above the app window instead of behind it. The script is one
// line of ';'-separated statements with single-quoted literals only, so it
// survives Windows command-line quoting untouched.
const psPrelude = "$ErrorActionPreference='Stop'; " +
	"try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding $false } catch {}; " +
	"Add-Type -AssemblyName System.Windows.Forms; " +
	"[System.Windows.Forms.Application]::EnableVisualStyles(); " +
	"$o = New-Object System.Windows.Forms.Form; $o.TopMost = $true; $o.ShowInTaskbar = $false; "

// psShow shows $d owned by $o and writes the chosen path as base64(UTF-8), so
// non-ASCII paths survive whatever console code page PowerShell ends up with.
func psShow(prop string) string {
	return "if ($d.ShowDialog($o) -eq [System.Windows.Forms.DialogResult]::OK) { " +
		"[Console]::Out.Write([Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($d." + prop + "))) }; " +
		"$d.Dispose(); $o.Dispose()"
}

func psFolderScript(title, start string) string {
	s := psPrelude +
		"$d = New-Object System.Windows.Forms.FolderBrowserDialog; " +
		"$d.Description = " + psQuote(title) + "; $d.ShowNewFolderButton = $true; "
	if start != "" {
		s += "$d.SelectedPath = " + psQuote(start) + "; "
	}
	return s + psShow("SelectedPath")
}

func psFileScript(title string, exts []string) string {
	return psPrelude +
		"$d = New-Object System.Windows.Forms.OpenFileDialog; " +
		"$d.Title = " + psQuote(title) + "; $d.Filter = " + psQuote(winFilter(exts)) + "; " +
		"$d.Multiselect = $false; $d.CheckFileExists = $true; $d.RestoreDirectory = $true; " +
		psShow("FileName")
}

func psSaveScript(title, dir, name, ext string) string {
	s := psPrelude +
		"$d = New-Object System.Windows.Forms.SaveFileDialog; " +
		"$d.Title = " + psQuote(title) + "; $d.Filter = " + psQuote(winSaveFilter(ext)) + "; " +
		"$d.OverwritePrompt = $true; $d.AddExtension = $true; $d.RestoreDirectory = $true; "
	if name != "" {
		s += "$d.FileName = " + psQuote(name) + "; "
	}
	if ext != "" {
		s += "$d.DefaultExt = " + psQuote(ext) + "; "
	}
	if dir != "" {
		s += "$d.InitialDirectory = " + psQuote(dir) + "; "
	}
	return s + psShow("FileName")
}

// psArgs is the powershell.exe argument list for a dialog script.
func psArgs(script string) []string {
	return []string{"-NoProfile", "-NonInteractive", "-STA", "-Command", script}
}

// psResult decodes the base64 path written by psShow ("" = cancelled).
func psResult(out []byte) string {
	s := dialogOutput(out)
	if s == "" {
		return ""
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return strings.TrimSpace(string(b))
	}
	return s // older helper / plain output
}

// winFilter builds an OpenFileDialog filter, e.g.
// "Підтримувані файли (*.mp3, *.wav)|*.mp3;*.wav|Усі файли (*.*)|*.*".
func winFilter(exts []string) string {
	all := labelAll + " (*.*)|*.*"
	if len(exts) == 0 {
		return all
	}
	pats := make([]string, len(exts))
	for i, e := range exts {
		pats[i] = "*." + e
	}
	return labelSupported + " (" + strings.Join(pats, ", ") + ")|" + strings.Join(pats, ";") + "|" + all
}

func winSaveFilter(ext string) string {
	all := labelAll + " (*.*)|*.*"
	if ext == "" {
		return all
	}
	return strings.ToUpper(ext) + " (*." + ext + ")|*." + ext + "|" + all
}

// ---- macOS: osascript ---------------------------------------------------------------

// osaArgs turns script lines + argv into osascript arguments. Values travel as
// argv (on run argv), never spliced into the script, so no escaping is needed.
func osaArgs(lines []string, argv ...string) []string {
	var args []string
	for _, l := range lines {
		args = append(args, "-e", l)
	}
	return append(args, argv...)
}

func osaFolderArgs(title, start string) []string {
	choose := "choose folder with prompt (item 1 of argv)"
	if start != "" {
		choose += " default location ((item 2 of argv) as POSIX file)"
	}
	return osaArgs([]string{
		"on run argv",
		"activate",
		"return POSIX path of (" + choose + ")",
		"end run",
	}, title, start)
}

func osaFileArgs(title string, exts []string) []string {
	lines := []string{"on run argv", "activate"}
	if len(exts) > 0 {
		lines = append(lines,
			"set exts to items 2 thru -1 of argv",
			"return POSIX path of (choose file with prompt (item 1 of argv) of type exts)")
	} else {
		lines = append(lines, "return POSIX path of (choose file with prompt (item 1 of argv))")
	}
	lines = append(lines, "end run")
	return osaArgs(lines, append([]string{title}, exts...)...)
}

func osaSaveArgs(title, dir, name string) []string {
	choose := "choose file name with prompt (item 1 of argv) default name (item 2 of argv)"
	if dir != "" {
		choose += " default location ((item 3 of argv) as POSIX file)"
	}
	return osaArgs([]string{
		"on run argv",
		"activate",
		"return POSIX path of (" + choose + ")",
		"end run",
	}, title, name, dir)
}

// osaCancelled reports the "User canceled. (-128)" failure.
func osaCancelled(stderr []byte) bool {
	return strings.Contains(string(stderr), "-128")
}

// ---- Linux: zenity, then kdialog ------------------------------------------------------

// globs lists *.ext and *.EXT (Linux dialogs match case-sensitively).
func globs(exts []string) []string {
	var g []string
	for _, e := range exts {
		g = append(g, "*."+e)
		if u := strings.ToUpper(e); u != e {
			g = append(g, "*."+u)
		}
	}
	return g
}

func zenityFilters(exts []string) []string {
	if len(exts) == 0 {
		return nil
	}
	return []string{
		"--file-filter=" + labelSupported + " | " + strings.Join(globs(exts), " "),
		"--file-filter=" + labelAll + " | *",
	}
}

func zenityFolderArgs(title, start string) []string {
	a := []string{"--file-selection", "--directory", "--title=" + title}
	if start != "" {
		a = append(a, "--filename="+strings.TrimRight(start, "/")+"/")
	}
	return a
}

func zenityFileArgs(title string, exts []string) []string {
	return append([]string{"--file-selection", "--title=" + title}, zenityFilters(exts)...)
}

func zenitySaveArgs(title, dir, name, ext string) []string {
	a := []string{"--file-selection", "--save", "--confirm-overwrite", "--title=" + title}
	if p := joinDirName(dir, name); p != "" {
		a = append(a, "--filename="+p)
	}
	if ext != "" {
		a = append(a, zenityFilters([]string{ext})...)
	}
	return a
}

func kdialogFilter(exts []string) string {
	if len(exts) == 0 {
		return "*|" + labelAll
	}
	return strings.Join(globs(exts), " ") + "|" + labelSupported
}

func kdialogFolderArgs(title, start, home string) []string {
	if start == "" {
		start = home
	}
	return []string{"--title", title, "--getexistingdirectory", start}
}

func kdialogFileArgs(title string, exts []string, home string) []string {
	return []string{"--title", title, "--getopenfilename", home, kdialogFilter(exts)}
}

func kdialogSaveArgs(title, dir, name, ext, home string) []string {
	if dir == "" {
		dir = home
	}
	var exts []string
	if ext != "" {
		exts = []string{ext}
	}
	return []string{"--title", title, "--getsavefilename", joinDirName(dir, name), kdialogFilter(exts)}
}

// joinDirName joins a POSIX dir and file name (Linux helpers only).
func joinDirName(dir, name string) string {
	if dir == "" {
		return name
	}
	return strings.TrimRight(dir, "/") + "/" + name
}
