package guard

import "testing"

// Each watcher arm ClassifyArm refuses in a Bash call is refused in the form
// PowerShell gives it, with the same code, and what names no watcher is
// allowed.
func TestClassifyPowerShellArm(t *testing.T) {
	tests := []struct {
		command  string
		wantCode string
	}{
		{command: "Write-Output hi"},
		{command: "git log --oneline"},
		{command: "cfo watchdog-config"},
		{command: `cfo send g1 "watch the logs"`},
		{command: "Get-Content watch.log"},
		{command: "Start-Process notepad"},
		{command: "cfo status; Get-Process cfo"},
		{command: "cfo watch", wantCode: "watcher-direct"},
		{command: `.\cfo.exe watch`, wantCode: "watcher-direct"},
		{command: `& "C:\dev\code-goblins\bin\cfo.exe" watch`, wantCode: "watcher-direct"},
		{command: `C:\dev\code-goblins\cfo.exe watch`, wantCode: "watcher-direct"},
		{command: "cfo wa`tch", wantCode: "watcher-direct"},
		{command: `.\bin\fm-watch.ps1`, wantCode: "watcher-direct"},
		{command: "cfo watch &", wantCode: "watcher-background"},
		{command: "Start-Process cfo -ArgumentList watch", wantCode: "watcher-background"},
		{command: `Start-Process -FilePath C:\dev\code-goblins\bin\cfo.exe -ArgumentList @('watch', '--once') -WindowStyle Hidden`, wantCode: "watcher-background"},
		{command: "Start-Process cfo -WindowStyle Hidden -ArgumentList watch", wantCode: "watcher-background"},
		{command: "$start = @{ FilePath = 'cfo'; ArgumentList = 'watch' }\nStart-Process @start", wantCode: "watcher-background"},
		{command: "saps cfo watch", wantCode: "watcher-background"},
		{command: "[Diagnostics.Process]::Start('cfo.exe', 'watch')", wantCode: "watcher-background"},
		{command: "Start-Job { cfo watch }", wantCode: "watcher-background"},
		{command: "cfo watch | Tee-Object log", wantCode: "watcher-pipeline"},
		{command: "cfo watch > out.txt", wantCode: "watcher-redirection"},
		{command: "cfo watch *> out.txt", wantCode: "watcher-redirection"},
		{command: "Set-Location x; cfo watch", wantCode: "watcher-bundled"},
		{command: "cfo status && cfo watch", wantCode: "watcher-bundled"},
		{command: "$(cfo watch)", wantCode: "watcher-nested"},
		{command: "& { cfo watch }", wantCode: "watcher-nested"},
		{command: "Invoke-Expression 'cfo watch'", wantCode: "watcher-nested"},
		{command: `powershell -Command "cfo watch"`, wantCode: "watcher-nested"},
		{command: `cmd /c "cfo watch"`, wantCode: "watcher-nested"},
		{command: "Stop-Process -Name cfo; cfo watch", wantCode: "broad-watcher-kill"},
		{command: "Get-CimInstance Win32_Process | Where-Object CommandLine -like '*cfo*watch*' | Stop-Process", wantCode: "broad-watcher-kill"},
		{command: "taskkill /f /im cfo.exe; cfo watch", wantCode: "broad-watcher-kill"},
		{command: "Write-Output @'\ncfo watch\n'@", wantCode: "unclassifiable-protected-command"},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			code, reason, deny := ClassifyPowerShellArm(tt.command)
			if deny != (tt.wantCode != "") || code != tt.wantCode {
				t.Fatalf("ClassifyPowerShellArm(%q) = %q, deny %v; want code %q", tt.command, code, deny, tt.wantCode)
			}
			if reason != armReasons[tt.wantCode] {
				t.Errorf("ClassifyPowerShellArm(%q) reason = %q, want %q", tt.command, reason, armReasons[tt.wantCode])
			}
		})
	}
}

// A relocation is refused wherever PowerShell runs it as a command, by any
// of its names, and a relocation word that is not a command is allowed.
func TestClassifyPowerShellCd(t *testing.T) {
	const wantReason = "Claude Code's PowerShell tool keeps its working directory between calls, so this relocation would outlive the tool call"

	tests := []struct {
		command  string
		wantDeny bool
	}{
		{command: `cd C:\other`, wantDeny: true},
		{command: `Set-Location C:\`, wantDeny: true},
		{command: `SET-LOCATION C:\`, wantDeny: true},
		{command: `sl C:\`, wantDeny: true},
		{command: `chdir C:\`, wantDeny: true},
		{command: "pushd ..", wantDeny: true},
		{command: "Push-Location ..", wantDeny: true},
		{command: "go test ./... ; popd", wantDeny: true},
		{command: "go test ./... ; Pop-Location", wantDeny: true},
		{command: "cd..", wantDeny: true},
		{command: `cd\`, wantDeny: true},
		{command: "D:", wantDeny: true},
		{command: "cd sub; go test ./...", wantDeny: true},
		{command: "go test ./... && cd sub", wantDeny: true},
		{command: "go test ./...\ncd sub", wantDeny: true},
		{command: "go test ./...\r\ncd sub", wantDeny: true},
		{command: "(cd sub) ; make", wantDeny: true},
		{command: "if (Test-Path sub) { cd sub }", wantDeny: true},
		{command: "if (Test-Path sub) { dir } cd sub", wantDeny: true},
		{command: "Get-Item sub | Set-Location", wantDeny: true},
		{command: "& 'Set-Location' sub", wantDeny: true},
		{command: `& "cd" sub`, wantDeny: true},
		{command: ". cd sub", wantDeny: true},
		{command: `Microsoft.PowerShell.Management\Set-Location sub`, wantDeny: true},
		{command: "$here = Set-Location sub -PassThru", wantDeny: true},
		{command: "c`d sub", wantDeny: true},
		{command: "Get-ChildItem `\n  -Path sub; cd sub", wantDeny: true},
		{command: "# don't forget the build\ncd sub", wantDeny: true},
		{command: "<# it's the build #> cd sub", wantDeny: true},
		{command: "$note = @'\ndon't\n'@\ncd sub", wantDeny: true},
		{command: "$note = @\"\nsay \"hi\"\n\"@\ncd sub", wantDeny: true},
		{command: `Write-Output "it is $(cd sub)"`, wantDeny: true},
		{command: "Write-Output 'it''s'; cd sub", wantDeny: true},
		{command: "Write-Output cd", wantDeny: false},
		{command: "Write-Output C:", wantDeny: false},
		{command: `git commit -m "wip; cd later"`, wantDeny: false},
		{command: "git commit -m 'cd; popd'", wantDeny: false},
		{command: `Write-Output "a | popd"`, wantDeny: false},
		{command: `git -C C:\x status`, wantDeny: false},
		{command: "go test ./...\ngo build ./...", wantDeny: false},
		{command: `powershell -Command "cd sub; dir"`, wantDeny: false},
		{command: `cfo send g1 "cd into the folder"`, wantDeny: false},
		{command: "$note = @'\ncd sub\n'@\nWrite-Output $note", wantDeny: false},
		{command: "# cd sub\ndir", wantDeny: false},
		{command: "dir # then; cd sub", wantDeny: false},
		{command: "Get-ChildItem -Path cd", wantDeny: false},
		{command: `Get-Content C:\x\cd.txt`, wantDeny: false},
		{command: "dotnet build --output=cd", wantDeny: false},
		{command: `$env:PATH = "C:\x;$env:PATH"`, wantDeny: false},
		{command: `& "C:\Program Files\tool\cd.exe" sub`, wantDeny: false},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			code, reason, deny := ClassifyPowerShellCd(tt.command)
			if deny != tt.wantDeny {
				t.Fatalf("ClassifyPowerShellCd(%q) deny = %v, want %v (code=%q reason=%q)", tt.command, deny, tt.wantDeny, code, reason)
			}
			if !tt.wantDeny {
				return
			}
			if code != "cwd-relocation" || reason != wantReason {
				t.Errorf("ClassifyPowerShellCd(%q) = %q, %q; want cwd-relocation, %q", tt.command, code, reason, wantReason)
			}
		})
	}
}
