package sbig

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// This is consumed by tools/abicheck/check.sh, rather than merely comparing Go
// constants with another set of Go constants. Clang supplies the independent
// platform layout and the vendor supplies the independent declarations.
func TestGenerateABICheck(t *testing.T) {
	path := os.Getenv("SBIG_ABI_CHECK_OUT")
	if path == "" {
		t.Skip("set SBIG_ABI_CHECK_OUT to generate vendor-header assertions")
	}
	var b strings.Builder
	b.WriteString("#include <stddef.h>\n#include \"sbigudrv.h\"\n")
	for _, l := range abiLayouts {
		fmt.Fprintf(&b, "_Static_assert(sizeof(%s) == %d, \"sizeof %s\");\n", l.name, l.size, l.name)
		var fields []string
		for name := range l.fields {
			fields = append(fields, name)
		}
		sort.Strings(fields)
		for _, f := range fields {
			fmt.Fprintf(&b, "_Static_assert(offsetof(%s, %s) == %d, \"offset %s.%s\");\n", l.name, f, l.off(f), l.name, f)
		}
	}
	for _, l := range []struct{ name, field string }{
		{"GetCCDInfoParams", "request"}, {"GetDriverInfoParams", "request"},
		{"QueryTemperatureStatusParams", "request"}, {"QueryCommandStatusParams", "command"},
		{"QueryCommandStatusResults", "status"}, {"GetDriverControlParams", "controlParameter"},
		{"EndExposureParams", "ccd"}, {"EndReadoutParams", "ccd"},
		{"EstablishLinkParams", "sbigUseOnly"}, {"EstablishLinkResults", "cameraType"},
		{"GetErrorStringParams", "errorNo"}, {"BitIOResults", "bitIsSet"},
	} {
		fmt.Fprintf(&b, "_Static_assert(sizeof(%s) == %d && offsetof(%s, %s) == 0, \"word %s\");\n", l.name, abiWordSize, l.name, l.field, l.name)
	}
	for _, f := range []struct{ goField, cField string }{
		{"mode", "mode"}, {"width", "width"}, {"height", "height"}, {"gain", "gain"},
		{"pixel_width", "pixelWidth"}, {"pixel_height", "pixelHeight"},
	} {
		fmt.Fprintf(&b, "_Static_assert(offsetof(GetCCDInfoResults0, readoutInfo[0].%s) == %d, \"CCD readout %s\");\n", f.cField, abiCCDInfo.off("readoutInfo")+abiReadoutInfo.off(f.goField), f.cField)
	}
	fmt.Fprintf(&b, "_Static_assert(sizeof(((GetCCDInfoResults0*)0)->readoutInfo[0]) == %d, \"readout stride\");\n", abiReadoutInfo.size)
	fmt.Fprintf(&b, "_Static_assert(sizeof(CustomerOptionsResults) == %d, \"customer results\");\n", abiCustomer.size)
	var ids []int
	for id := range commandNames {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	for _, id := range ids {
		fmt.Fprintf(&b, "_Static_assert(%s == %d, \"command %s\");\n", commandNames[uint16(id)], id, commandNames[uint16(id)])
	}
	b.WriteString("_Static_assert(CE_UNKNOWN_COMMAND == 4 && CE_BAD_CAMERA_COMMAND == 5, \"optional command errors\");\n")
	b.WriteString("_Static_assert(XES_IDLE == 0 && XES_PRE_EXP == 1 && XES_INTEGRATING == 2 && XES_POST_EXP == 3, \"phase values\");\n")
	if err := os.WriteFile(path, []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}
}
