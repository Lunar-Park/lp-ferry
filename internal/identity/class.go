package identity

import "strings"

// ClassFromSysfs maps Linux queue/rotational and removable to a class.
// mmcblk devices are SD/eMMC cards whatever ROTA says.
func ClassFromSysfs(rotational, removable, devName string) StorageClass {
	switch {
	case strings.HasPrefix(devName, "mmcblk"), removable == "1":
		return ClassRemovable
	case rotational == "1":
		return ClassHDD
	case rotational == "0":
		return ClassSSD
	}
	return ClassUnknown
}
