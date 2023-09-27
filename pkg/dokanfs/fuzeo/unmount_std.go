//go:build !linux

package fuzeo

import (
	"fmt"
)

func unmount(dir string) error {
	// err := syscall.Unmount(dir, 0)
	// if err != nil {
	// 	err = &os.PathError{Op: "unmount", Path: dir, Err: err}
	// 	return err
	// }
	// return nil
	return fmt.Errorf("not supported")
}
