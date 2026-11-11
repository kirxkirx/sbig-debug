//go:build !windows

package sbig

func Inventory(requested string) []DLLInfo {
	if requested != "" {
		return []DLLInfo{inspectDLL(requested, "explicit; native loading requires Windows")}
	}
	return nil
}
