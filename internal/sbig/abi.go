package sbig

// These layouts drive the actual byte-buffer packing and decoding. The ABI test
// generates C assertions from the same values and compares them with the pinned
// manufacturer's header for both Windows targets. Go's native struct alignment
// is deliberately not used (notably for double fields on 32-bit Windows).
type abiLayout struct {
	name   string
	size   int
	fields map[string]int
}

func (l abiLayout) off(name string) int {
	n, ok := l.fields[name]
	if !ok {
		panic("unknown ABI field " + l.name + "." + name)
	}
	return n
}

const abiWordSize = 2

var (
	abiOpenDevice        = abiLayout{"OpenDeviceParams", 8, map[string]int{"deviceType": 0, "lptBaseAddress": 2, "ipAddress": 4}}
	abiSetControl        = abiLayout{"SetDriverControlParams", 8, map[string]int{"controlParameter": 0, "controlValue": 4}}
	abiGetControl        = abiLayout{"GetDriverControlResults", 4, map[string]int{"controlValue": 0}}
	abiCCDInfo           = abiLayout{"GetCCDInfoResults0", 392, map[string]int{"firmwareVersion": 0, "cameraType": 2, "name": 4, "readoutModes": 68, "readoutInfo": 72}}
	abiReadoutInfo       = abiLayout{"READOUT_INFO", 16, map[string]int{"mode": 0, "width": 2, "height": 4, "gain": 6, "pixel_width": 8, "pixel_height": 12}}
	abiDriverInfo        = abiLayout{"GetDriverInfoResults0", 68, map[string]int{"version": 0, "name": 2, "maxRequest": 66}}
	abiExtended          = abiLayout{"GetCCDInfoResults2", 22, map[string]int{"badColumns": 0, "columns": 2, "imagingABG": 10, "serialNumber": 12}}
	abiCapabilities      = abiLayout{"GetCCDInfoResults4", 4, map[string]int{"capabilitiesBits": 0, "dumpExtra": 2}}
	abiExtended3         = abiLayout{"GetCCDInfoResults6", 12, map[string]int{"cameraBits": 0, "ccdBits": 4, "extraBits": 8}}
	abiLink              = abiLayout{"GetLinkStatusResults", 16, map[string]int{"linkEstablished": 0, "baseAddress": 2, "cameraType": 4, "comTotal": 8, "comFailed": 12}}
	abiCustomer          = abiLayout{"CustomerOptionsParams", 10, map[string]int{"bSetCustomerOptions": 0, "bOverscanRegions": 2, "bWindowHeater": 4, "bPreflashCcd": 6, "bVddNormallyOff": 8}}
	abiBit               = abiLayout{"BitIOParams", 6, map[string]int{"bitOperation": 0, "bitName": 2, "setBit": 4}}
	abiTemperature       = abiLayout{"QueryTemperatureStatusResults2", 104, map[string]int{"coolingEnabled": 0, "fanEnabled": 2, "ccdSetpoint": 8, "imagingCCDTemperature": 16, "trackingCCDTemperature": 24, "externalTrackingCCDTemperature": 32, "ambientTemperature": 40, "imagingCCDPower": 48, "trackingCCDPower": 56, "externalTrackingCCDPower": 64, "heatsinkTemperature": 72, "fanPower": 80, "fanSpeed": 88, "trackingCCDSetpoint": 96}}
	abiTemperatureLegacy = abiLayout{"QueryTemperatureStatusResults", 10, map[string]int{"enabled": 0, "ccdSetpoint": 2, "power": 4, "ccdThermistor": 6, "ambientThermistor": 8}}
	abiExposure          = abiLayout{"StartExposureParams2", 24, map[string]int{"ccd": 0, "exposureTime": 4, "abgState": 8, "openShutter": 10, "readoutMode": 12, "top": 14, "left": 16, "height": 18, "width": 20}}
	abiReadout           = abiLayout{"StartReadoutParams", 12, map[string]int{"ccd": 0, "readoutMode": 2, "top": 4, "left": 6, "height": 8, "width": 10}}
	abiLine              = abiLayout{"ReadoutLineParams", 8, map[string]int{"ccd": 0, "readoutMode": 2, "pixelStart": 4, "pixelLength": 6}}
	abiStatus2           = abiLayout{"QueryCommandStatusResults2", 4, map[string]int{"status": 0, "info": 2}}
	abiError             = abiLayout{"GetErrorStringResults", 64, map[string]int{"errorString": 0}}
)

var abiLayouts = []abiLayout{
	abiOpenDevice, abiSetControl, abiGetControl, abiCCDInfo, abiReadoutInfo,
	abiDriverInfo, abiExtended, abiCapabilities, abiExtended3, abiLink, abiCustomer,
	abiBit, abiTemperature, abiTemperatureLegacy, abiExposure, abiReadout, abiLine,
	abiStatus2, abiError,
}
