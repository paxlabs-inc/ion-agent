//go:build linux

package project

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

func openProjectPTY(columns, rows uint16) (*os.File, *os.File, error) {
	masterFD, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	master := os.NewFile(uintptr(masterFD), "/dev/ptmx")
	unlock := 0
	if err := unix.IoctlSetPointerInt(masterFD, unix.TIOCSPTLCK, unlock); err != nil {
		_ = master.Close()
		return nil, nil, err
	}
	number, err := unix.IoctlGetInt(masterFD, unix.TIOCGPTN)
	if err != nil {
		_ = master.Close()
		return nil, nil, err
	}
	slave, err := os.OpenFile(filepath.Join("/dev/pts", strconv.Itoa(number)), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = master.Close()
		return nil, nil, err
	}
	if columns == 0 {
		columns = 80
	}
	if rows == 0 {
		rows = 24
	}
	if err := unix.IoctlSetWinsize(masterFD, unix.TIOCSWINSZ, &unix.Winsize{Col: columns, Row: rows}); err != nil {
		_ = slave.Close()
		_ = master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}

func resizeProjectPTY(file *os.File, columns, rows uint16) error {
	return unix.IoctlSetWinsize(int(file.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: columns, Row: rows})
}

func projectPTYSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
}
