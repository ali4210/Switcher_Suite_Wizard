//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	switcherKernel32Dll                     = syscall.NewLazyDLL("kernel32.dll")
	switcherProcGetConsoleScreenBufferInfo  = switcherKernel32Dll.NewProc("GetConsoleScreenBufferInfo")
	switcherProcGetLargestConsoleWindowSize = switcherKernel32Dll.NewProc("GetLargestConsoleWindowSize")
	switcherProcSetConsoleWindowInfo        = switcherKernel32Dll.NewProc("SetConsoleWindowInfo")
	switcherProcSetConsoleScreenBufferSize  = switcherKernel32Dll.NewProc("SetConsoleScreenBufferSize")
)

type consoleScreenBufferInfo struct {
	Size              windows.Coord
	CursorPosition    windows.Coord
	Attributes        uint16
	Window            windows.SmallRect
	MaximumWindowSize windows.Coord
}

func ensureWindowsConsole() {
	stdout := windows.Handle(os.Stdout.Fd())

	// Enable ANSI / Virtual Terminal Processing
	var mode uint32
	if err := windows.GetConsoleMode(stdout, &mode); err == nil {
		mode |= windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING | windows.ENABLE_PROCESSED_OUTPUT
		_ = windows.SetConsoleMode(stdout, mode)
	}

	// Read maximum allowable console window size for current display resolution
	rMax, _, _ := switcherProcGetLargestConsoleWindowSize.Call(uintptr(stdout))
	maxCols := int16(rMax & 0xFFFF)
	maxRows := int16((rMax >> 16) & 0xFFFF)

	if maxCols <= 0 || maxRows <= 0 {
		return
	}

	targetCols := int16(110)
	targetRows := int16(45)

	if targetCols > maxCols {
		targetCols = maxCols - 2
	}
	if targetRows > maxRows {
		targetRows = maxRows - 2
	}

	var csbi consoleScreenBufferInfo
	rInfo, _, _ := switcherProcGetConsoleScreenBufferInfo.Call(uintptr(stdout), uintptr(unsafe.Pointer(&csbi)))
	if rInfo == 0 {
		return
	}

	// Expand buffer if current buffer is smaller than target
	bufCols := csbi.Size.X
	bufRows := csbi.Size.Y
	if bufCols < targetCols {
		bufCols = targetCols
	}
	if bufRows < targetRows {
		bufRows = targetRows
	}

	packedBuf := uintptr(uint16(bufCols)) | uintptr(uint16(bufRows))<<16
	rBuf, _, _ := switcherProcSetConsoleScreenBufferSize.Call(uintptr(stdout), packedBuf)
	if rBuf == 0 {
		// Resizing the buffer failed (e.g. a legacy conhost quirk, or a
		// window currently maximized/pinned by the shell hosting us) --
		// leave the window alone too rather than request a window size
		// Win32 would then also reject (SetConsoleWindowInfo requires
		// the window to fit within the buffer), which could otherwise
		// leave buffer and window sizes in a mismatched state. Nothing
		// here is worse off than before this function ran: the console
		// simply keeps whatever size it already had.
		return
	}

	// Safely expand visible window without shrinking to 1x1 first
	rect := windows.SmallRect{
		Left:   0,
		Top:    0,
		Right:  targetCols - 1,
		Bottom: targetRows - 1,
	}
	rWin, _, _ := switcherProcSetConsoleWindowInfo.Call(uintptr(stdout), 1, uintptr(unsafe.Pointer(&rect)))
	if rWin == 0 {
		// Best-effort: the buffer is already at the target size even if
		// the visible window couldn't be expanded to match (e.g. the
		// physical display is smaller than targetCols/targetRows would
		// need). Nothing further to do.
		return
	}
}
