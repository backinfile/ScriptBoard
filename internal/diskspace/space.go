package diskspace

import "fmt"

const MinimumWritableBytes uint64 = 100 << 20

func Require(path string, minimum uint64) error {
	available, err := Available(path)
	if err != nil {
		return fmt.Errorf("检查磁盘可用空间: %w", err)
	}
	if available < minimum {
		return fmt.Errorf("磁盘可用空间不足：剩余 %d 字节，至少需要 %d 字节", available, minimum)
	}
	return nil
}
