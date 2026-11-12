//go:build !(darwin || linux)

package limits

import "errors"

func setNice(int) error { return errors.New("priority not supported here") }
