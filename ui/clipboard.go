package ui

import (
	"encoding/binary"
	"fmt"
	"os/exec"
	"runtime"
	"unicode/utf16"
)

// copyText puts s on the system clipboard via the platform's native tool.
func copyText(s string) error {
	switch runtime.GOOS {
	case "windows":
		// clip.exe interprets UTF-16 when the input starts with a BOM.
		runes := utf16.Encode([]rune(s))
		buf := make([]byte, 2+len(runes)*2)
		buf[0], buf[1] = 0xFF, 0xFE
		for i, r := range runes {
			binary.LittleEndian.PutUint16(buf[2+i*2:], r)
		}
		cmd := exec.Command("cmd", "/c", "clip")
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return err
		}
		if err := cmd.Start(); err != nil {
			return err
		}
		stdin.Write(buf)
		stdin.Close()
		return cmd.Wait()
	case "darwin":
		return pipeTo(exec.Command("pbcopy"), s)
	default:
		if _, err := exec.LookPath("wl-copy"); err == nil {
			return pipeTo(exec.Command("wl-copy"), s)
		}
		return pipeTo(exec.Command("xclip", "-selection", "clipboard"), s)
	}
}

func pipeTo(cmd *exec.Cmd, content string) error {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if _, err := fmt.Fprint(stdin, content); err != nil {
		stdin.Close()
		return err
	}
	stdin.Close()
	return cmd.Wait()
}

// openBrowser opens u in the default web browser.
func openBrowser(u string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	case "darwin":
		return exec.Command("open", u).Start()
	default:
		return exec.Command("xdg-open", u).Start()
	}
}
