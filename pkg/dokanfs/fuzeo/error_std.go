package fuzeo

import "syscall"

const (
	ENODATA = Errno(syscall.ENODATA)
)

// ErrNoXattr is a platform-independent error value meaning the
// extended attribute was not found. It can be used to respond to
// GetxattrRequest and such.
const ErrNoXattr = ENODATA

var _ error = ErrNoXattr
var _ Errno = ErrNoXattr
var _ ErrorNumber = ErrNoXattr
