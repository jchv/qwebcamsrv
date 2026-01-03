//go:build windows

package main

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"sync"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

const DefaultDeviceID = ""

var (
	ole32    = syscall.NewLazyDLL("ole32.dll")
	mfplat   = syscall.NewLazyDLL("mfplat.dll")
	mf       = syscall.NewLazyDLL("mf.dll")
	mfreadwr = syscall.NewLazyDLL("mfreadwrite.dll")

	procCoInitializeEx                      = ole32.NewProc("CoInitializeEx")
	procCoUninitialize                      = ole32.NewProc("CoUninitialize")
	procCoTaskMemFree                       = ole32.NewProc("CoTaskMemFree")
	procMFStartup                           = mfplat.NewProc("MFStartup")
	procMFShutdown                          = mfplat.NewProc("MFShutdown")
	procMFCreateAttributes                  = mfplat.NewProc("MFCreateAttributes")
	procMFEnumDeviceSources                 = mf.NewProc("MFEnumDeviceSources")
	procMFCreateSourceReaderFromMediaSource = mfreadwr.NewProc("MFCreateSourceReaderFromMediaSource")
)

const (
	coinitMultithreaded = 0x0
	mfVersion           = 0x00020070 // MF_SDK_VERSION << 16 | MF_API_VERSION
	mfStartupLite       = 0x1

	mfSourceReaderFirstVideoStream = 0xFFFFFFFC
)

type windowsCamera struct {
	reader     *mfSourceReader
	source     *mfMediaSource
	deviceName string
	width      uint32
	height     uint32
	stride     uint32
	isMJPEG    bool
	isRGB      bool
	mu         sync.Mutex
}

type msGUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

func (g *msGUID) equals(other *msGUID) bool {
	return g.Data1 == other.Data1 &&
		g.Data2 == other.Data2 &&
		g.Data3 == other.Data3 &&
		g.Data4 == other.Data4
}

var (
	guidMFDevsourceAttributeSourceType = msGUID{
		Data1: 0xc60ac5fe, Data2: 0x252a, Data3: 0x478f,
		Data4: [8]byte{0xa0, 0xef, 0xbc, 0x8f, 0xa5, 0xf7, 0xca, 0xd3},
	}
	guidMFDevsourceAttributeSourceTypeVidcapGuid = msGUID{
		Data1: 0x8ac3587a, Data2: 0x4ae7, Data3: 0x42d8,
		Data4: [8]byte{0x99, 0xe0, 0x0a, 0x60, 0x13, 0xee, 0xf9, 0x0f},
	}
	guidMFDevsourceAttributeSourceTypeVidcapSymbolicLink = msGUID{
		Data1: 0x58f0aad8, Data2: 0x22bf, Data3: 0x4f8a,
		Data4: [8]byte{0xbb, 0x3d, 0xd2, 0xc4, 0x97, 0x8c, 0x6e, 0x2f},
	}
	guidMFDevsourceAttributeFriendlyName = msGUID{
		Data1: 0x60d0e559, Data2: 0x52f8, Data3: 0x4fa2,
		Data4: [8]byte{0xbb, 0xce, 0xac, 0xdb, 0x34, 0xa8, 0xec, 0x1},
	}
	guidMFMtMajorType = msGUID{
		Data1: 0x48eba18e, Data2: 0xf8c9, Data3: 0x4687,
		Data4: [8]byte{0xbf, 0x11, 0x0a, 0x74, 0xc9, 0xf9, 0x6a, 0x8f},
	}
	guidMFMtSubtype = msGUID{
		Data1: 0xf7e34c9a, Data2: 0x42e8, Data3: 0x4714,
		Data4: [8]byte{0xb7, 0x4b, 0xcb, 0x29, 0xd7, 0x2c, 0x35, 0xe5},
	}
	guidMFMtFrameSize = msGUID{
		Data1: 0x1652c33d, Data2: 0xd6b2, Data3: 0x4012,
		Data4: [8]byte{0xb8, 0x34, 0x72, 0x03, 0x08, 0x49, 0xa3, 0x7d},
	}
	guidMFVideoFormatRGB24 = msGUID{
		Data1: 0x00000014, Data2: 0x0000, Data3: 0x0010,
		Data4: [8]byte{0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71},
	}
	guidMFVideoFormatRGB32 = msGUID{
		Data1: 0x00000016, Data2: 0x0000, Data3: 0x0010,
		Data4: [8]byte{0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71},
	}
	guidMFVideoFormatMJPG = msGUID{
		Data1: 0x47504a4d, Data2: 0x0000, Data3: 0x0010,
		Data4: [8]byte{0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71},
	}
	guidIIDIMFMediaSource = msGUID{
		Data1: 0x279a808d, Data2: 0xaec7, Data3: 0x40c8,
		Data4: [8]byte{0x9c, 0x6b, 0xa6, 0xb4, 0x92, 0xc7, 0x8a, 0x66},
	}
)

type iunknownVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
}

type iunknown struct {
	Vtbl *iunknownVtbl
}

type imfAttributesVtbl struct {
	iunknownVtbl
	GetItem            uintptr
	GetItemType        uintptr
	CompareItem        uintptr
	Compare            uintptr
	GetUINT32          uintptr
	GetUINT64          uintptr
	GetDouble          uintptr
	GetGUID            uintptr
	GetStringLength    uintptr
	GetString          uintptr
	GetAllocatedString uintptr
	GetBlobSize        uintptr
	GetBlob            uintptr
	GetAllocatedBlob   uintptr
	GetUnknown         uintptr
	SetItem            uintptr
	DeleteItem         uintptr
	DeleteAllItems     uintptr
	SetUINT32          uintptr
	SetUINT64          uintptr
	SetDouble          uintptr
	SetGUID            uintptr
	SetString          uintptr
	SetBlob            uintptr
	SetUnknown         uintptr
	LockStore          uintptr
	UnlockStore        uintptr
	GetCount           uintptr
	GetItemByIndex     uintptr
	CopyAllItems       uintptr
}

type imfActivateVtbl struct {
	imfAttributesVtbl
	ActivateObject uintptr
	ShutdownObject uintptr
	DetachObject   uintptr
}

type imfMediaTypeVtbl struct {
	imfAttributesVtbl
	GetMajorType       uintptr
	IsCompressedFormat uintptr
	IsEqual            uintptr
	GetRepresentation  uintptr
	FreeRepresentation uintptr
}

type imfMediaBufferVtbl struct {
	iunknownVtbl
	Lock             uintptr
	Unlock           uintptr
	GetCurrentLength uintptr
	SetCurrentLength uintptr
	GetMaxLength     uintptr
}

type imfSampleVtbl struct {
	imfAttributesVtbl
	GetSampleFlags            uintptr
	SetSampleFlags            uintptr
	GetSampleTime             uintptr
	SetSampleTime             uintptr
	GetSampleDuration         uintptr
	SetSampleDuration         uintptr
	GetBufferCount            uintptr
	GetBufferByIndex          uintptr
	ConvertToContiguousBuffer uintptr
	AddBuffer                 uintptr
	RemoveBufferByIndex       uintptr
	RemoveAllBuffers          uintptr
	GetTotalLength            uintptr
	CopyToBuffer              uintptr
}

type imfSourceReaderVtbl struct {
	iunknownVtbl
	GetStreamSelection       uintptr
	SetStreamSelection       uintptr
	GetNativeMediaType       uintptr
	GetCurrentMediaType      uintptr
	SetCurrentMediaType      uintptr
	SetCurrentPosition       uintptr
	ReadSample               uintptr
	Flush                    uintptr
	GetServiceForStream      uintptr
	GetPresentationAttribute uintptr
}

type imfMediaSourceVtbl struct {
	iunknownVtbl
	GetEvent                     uintptr
	BeginGetEvent                uintptr
	EndGetEvent                  uintptr
	QueueEvent                   uintptr
	GetCharacteristics           uintptr
	CreatePresentationDescriptor uintptr
	Start                        uintptr
	Stop                         uintptr
	Pause                        uintptr
	Shutdown                     uintptr
}

type comObject struct {
	ptr *iunknown
}

func (o *comObject) addRef() uint32 {
	if o.ptr == nil {
		return 0
	}
	ret, _, _ := syscall.SyscallN(o.ptr.Vtbl.AddRef, uintptr(unsafe.Pointer(o.ptr)))
	return uint32(ret)
}

func (o *comObject) release() uint32 {
	if o.ptr == nil {
		return 0
	}
	ret, _, _ := syscall.SyscallN(o.ptr.Vtbl.Release, uintptr(unsafe.Pointer(o.ptr)))
	return uint32(ret)
}

// IMFAttributes wrapper
type mfAttributes struct {
	comObject
}

func (a *mfAttributes) vtbl() *imfAttributesVtbl {
	return *(**imfAttributesVtbl)(unsafe.Pointer(a.ptr))
}

func (a *mfAttributes) setGUID(key, value *msGUID) error {
	hr, _, _ := syscall.SyscallN(a.vtbl().SetGUID, uintptr(unsafe.Pointer(a.ptr)),
		uintptr(unsafe.Pointer(key)),
		uintptr(unsafe.Pointer(value)))
	if hr != 0 {
		return fmt.Errorf("SetGUID failed: 0x%08x", hr)
	}
	return nil
}

func (a *mfAttributes) getGUID(key *msGUID) (*msGUID, error) {
	var result msGUID

	hr, _, _ := syscall.SyscallN(a.vtbl().GetGUID, uintptr(unsafe.Pointer(a.ptr)),
		uintptr(unsafe.Pointer(key)),
		uintptr(unsafe.Pointer(&result)))
	if hr != 0 {
		return nil, fmt.Errorf("GetGUID failed: 0x%08x", hr)
	}
	return &result, nil
}

func (a *mfAttributes) getUINT64(key *msGUID) (uint64, error) {
	var result uint64

	hr, _, _ := syscall.SyscallN(a.vtbl().GetUINT64, uintptr(unsafe.Pointer(a.ptr)),
		uintptr(unsafe.Pointer(key)),
		uintptr(unsafe.Pointer(&result)))
	if hr != 0 {
		return 0, fmt.Errorf("GetUINT64 failed: 0x%08x", hr)
	}
	return result, nil
}

func (a *mfAttributes) getAllocatedString(key *msGUID) (string, error) {
	var pwstr *uint16
	var length uint32

	hr, _, _ := syscall.SyscallN(a.vtbl().GetAllocatedString, uintptr(unsafe.Pointer(a.ptr)),
		uintptr(unsafe.Pointer(key)),
		uintptr(unsafe.Pointer(&pwstr)),
		uintptr(unsafe.Pointer(&length)))
	if hr != 0 {
		return "", fmt.Errorf("GetAllocatedString failed: 0x%08x", hr)
	}
	if pwstr == nil {
		return "", nil
	}
	defer func() {
		if pwstr != nil {
			syscall.SyscallN(procCoTaskMemFree.Addr(), uintptr(unsafe.Pointer(pwstr)))
		}
	}()

	return utf16PtrToString(pwstr, int(length)), nil
}

type mfActivate struct {
	mfAttributes
}

func (a *mfActivate) vtbl() *imfActivateVtbl {
	return *(**imfActivateVtbl)(unsafe.Pointer(a.ptr))
}

func (a *mfActivate) activateObject(iid *msGUID) (*iunknown, error) {
	var obj *iunknown

	hr, _, _ := syscall.SyscallN(a.vtbl().ActivateObject, uintptr(unsafe.Pointer(a.ptr)),
		uintptr(unsafe.Pointer(iid)),
		uintptr(unsafe.Pointer(&obj)))
	if hr != 0 {
		return nil, fmt.Errorf("ActivateObject failed: 0x%08x", hr)
	}
	return obj, nil
}

type mfMediaType struct {
	mfAttributes
}

func (t *mfMediaType) vtbl() *imfMediaTypeVtbl {
	return *(**imfMediaTypeVtbl)(unsafe.Pointer(t.ptr))
}

type mfMediaBuffer struct {
	comObject
}

func (b *mfMediaBuffer) vtbl() *imfMediaBufferVtbl {
	return *(**imfMediaBufferVtbl)(unsafe.Pointer(b.ptr))
}

func (b *mfMediaBuffer) lock() ([]byte, error) {
	var pBuffer *byte
	var maxLen, curLen uint32

	hr, _, _ := syscall.SyscallN(b.vtbl().Lock, uintptr(unsafe.Pointer(b.ptr)),
		uintptr(unsafe.Pointer(&pBuffer)),
		uintptr(unsafe.Pointer(&maxLen)),
		uintptr(unsafe.Pointer(&curLen)))
	if hr != 0 {
		return nil, fmt.Errorf("Lock failed: 0x%08x", hr)
	}

	// Create a slice that views the buffer memory
	// WARNING: This slice is only valid until Unlock is called
	return unsafe.Slice(pBuffer, curLen), nil
}

func (b *mfMediaBuffer) unlock() error {
	hr, _, _ := syscall.SyscallN(b.vtbl().Unlock, uintptr(unsafe.Pointer(b.ptr)))
	if hr != 0 {
		return fmt.Errorf("Unlock failed: 0x%08x", hr)
	}
	return nil
}

type mfSample struct {
	mfAttributes
}

func (s *mfSample) vtbl() *imfSampleVtbl {
	return *(**imfSampleVtbl)(unsafe.Pointer(s.ptr))
}

func (s *mfSample) convertToContiguousBuffer() (*mfMediaBuffer, error) {
	var buffer *iunknown

	hr, _, _ := syscall.SyscallN(s.vtbl().ConvertToContiguousBuffer, uintptr(unsafe.Pointer(s.ptr)),
		uintptr(unsafe.Pointer(&buffer)))
	if hr != 0 {
		return nil, fmt.Errorf("ConvertToContiguousBuffer failed: 0x%08x", hr)
	}
	return &mfMediaBuffer{comObject{buffer}}, nil
}

type mfSourceReader struct {
	comObject
}

func (r *mfSourceReader) vtbl() *imfSourceReaderVtbl {
	return *(**imfSourceReaderVtbl)(unsafe.Pointer(r.ptr))
}

func (r *mfSourceReader) getNativeMediaType(streamIndex, typeIndex uint32) (*mfMediaType, error) {
	var mediaType *iunknown
	hr, _, _ := syscall.SyscallN(r.vtbl().GetNativeMediaType, uintptr(unsafe.Pointer(r.ptr)),
		uintptr(streamIndex),
		uintptr(typeIndex),
		uintptr(unsafe.Pointer(&mediaType)))
	if hr != 0 {
		return nil, fmt.Errorf("GetNativeMediaType failed: 0x%08x", hr)
	}
	return &mfMediaType{mfAttributes{comObject{mediaType}}}, nil
}

func (r *mfSourceReader) getCurrentMediaType(streamIndex uint32) (*mfMediaType, error) {
	var mediaType *iunknown
	hr, _, _ := syscall.SyscallN(r.vtbl().GetCurrentMediaType, uintptr(unsafe.Pointer(r.ptr)), uintptr(streamIndex), uintptr(unsafe.Pointer(&mediaType)))
	if hr != 0 {
		return nil, fmt.Errorf("GetCurrentMediaType failed: 0x%08x", hr)
	}
	return &mfMediaType{mfAttributes{comObject{mediaType}}}, nil
}

func (r *mfSourceReader) setCurrentMediaType(streamIndex uint32, mediaType *mfMediaType) error {
	hr, _, _ := syscall.SyscallN(r.vtbl().SetCurrentMediaType, uintptr(unsafe.Pointer(r.ptr)), uintptr(streamIndex), 0, uintptr(unsafe.Pointer(mediaType.ptr)))
	if hr != 0 {
		return fmt.Errorf("SetCurrentMediaType failed: 0x%08x", hr)
	}
	return nil
}

func (r *mfSourceReader) readSample(streamIndex, flags uint32) (*mfSample, uint32, uint32, int64, error) {
	var actualIndex, sampleFlags uint32
	var timestamp int64
	var sample *iunknown

	hr, _, _ := syscall.SyscallN(r.vtbl().ReadSample, uintptr(unsafe.Pointer(r.ptr)),
		uintptr(streamIndex),
		uintptr(flags),
		uintptr(unsafe.Pointer(&actualIndex)),
		uintptr(unsafe.Pointer(&sampleFlags)),
		uintptr(unsafe.Pointer(&timestamp)),
		uintptr(unsafe.Pointer(&sample)))
	if hr != 0 {
		return nil, 0, 0, 0, fmt.Errorf("ReadSample failed: 0x%08x", hr)
	}

	var sampleObj *mfSample
	if sample != nil {
		sampleObj = &mfSample{mfAttributes{comObject{sample}}}
	}
	return sampleObj, actualIndex, sampleFlags, timestamp, nil
}

type mfMediaSource struct {
	comObject
}

func (s *mfMediaSource) vtbl() *imfMediaSourceVtbl {
	return *(**imfMediaSourceVtbl)(unsafe.Pointer(s.ptr))
}

func (s *mfMediaSource) shutdown() error {
	hr, _, _ := syscall.SyscallN(s.vtbl().Shutdown, uintptr(unsafe.Pointer(s.ptr)))
	if hr != 0 {
		return fmt.Errorf("Shutdown failed: 0x%08x", hr)
	}
	return nil
}

func utf16PtrToString(ptr *uint16, maxLen int) string {
	if ptr == nil {
		return ""
	}
	chars := make([]uint16, 0, maxLen)
	for i := range maxLen {
		c := *(*uint16)(unsafe.Pointer(uintptr(unsafe.Pointer(ptr)) + uintptr(i*2)))
		if c == 0 {
			break
		}
		chars = append(chars, c)
	}
	return string(utf16.Decode(chars))
}

func stringToUTF16Ptr(s string) *uint16 {
	u16 := utf16.Encode([]rune(s + "\x00"))
	return &u16[0]
}

var (
	mfInitMu    sync.Mutex
	mfInitCount int
)

func initMF() error {
	mfInitMu.Lock()
	defer mfInitMu.Unlock()

	if mfInitCount == 0 {
		hr, _, _ := syscall.SyscallN(procCoInitializeEx.Addr(), 0, coinitMultithreaded)
		// S_OK or S_FALSE (already initialized) are both acceptable
		if hr != 0 && hr != 1 {
			return fmt.Errorf("CoInitializeEx failed: 0x%08x", hr)
		}
		hr, _, _ = syscall.SyscallN(procMFStartup.Addr(), mfVersion, mfStartupLite)
		if hr != 0 {
			return fmt.Errorf("MFStartup failed: 0x%08x", hr)
		}
	}
	mfInitCount++
	return nil
}

func shutdownMF() {
	mfInitMu.Lock()
	defer mfInitMu.Unlock()

	mfInitCount--
	if mfInitCount == 0 {
		syscall.SyscallN(procMFShutdown.Addr())
		syscall.SyscallN(procCoUninitialize.Addr())
	}
}

func createAttributes(initialSize uint32) (*mfAttributes, error) {
	var attrs *iunknown

	hr, _, _ := syscall.SyscallN(procMFCreateAttributes.Addr(),
		uintptr(unsafe.Pointer(&attrs)),
		uintptr(initialSize))
	if hr != 0 {
		return nil, fmt.Errorf("MFCreateAttributes failed: 0x%08x", hr)
	}
	return &mfAttributes{comObject{attrs}}, nil
}

func enumDeviceSources(attrs *mfAttributes) ([]*mfActivate, error) {
	var ppDevices **iunknown
	var count uint32

	hr, _, _ := syscall.SyscallN(procMFEnumDeviceSources.Addr(),
		uintptr(unsafe.Pointer(attrs.ptr)),
		uintptr(unsafe.Pointer(&ppDevices)),
		uintptr(unsafe.Pointer(&count)))
	if hr != 0 {
		return nil, fmt.Errorf("MFEnumDeviceSources failed: 0x%08x", hr)
	}
	if count == 0 || ppDevices == nil {
		return nil, nil
	}
	devices := make([]*mfActivate, count)
	devicePtrs := unsafe.Slice(ppDevices, count)
	for i := uint32(0); i < count; i++ {
		devices[i] = &mfActivate{mfAttributes{comObject{devicePtrs[i]}}}
	}
	if ppDevices != nil {
		syscall.SyscallN(procCoTaskMemFree.Addr(), uintptr(unsafe.Pointer(ppDevices)))
	}
	return devices, nil
}

func createSourceReaderFromMediaSource(source *mfMediaSource, attrs *mfAttributes) (*mfSourceReader, error) {
	var hr uintptr
	var reader *iunknown

	if attrs != nil && attrs.ptr != nil {
		hr, _, _ = syscall.SyscallN(procMFCreateSourceReaderFromMediaSource.Addr(),
			uintptr(unsafe.Pointer(source.ptr)),
			uintptr(unsafe.Pointer(attrs.ptr)),
			uintptr(unsafe.Pointer(&reader)))
	} else {
		hr, _, _ = syscall.SyscallN(procMFCreateSourceReaderFromMediaSource.Addr(),
			uintptr(unsafe.Pointer(source.ptr)),
			uintptr(0),
			uintptr(unsafe.Pointer(&reader)))
	}
	if hr != 0 {
		return nil, fmt.Errorf("MFCreateSourceReaderFromMediaSource failed: 0x%08x", hr)
	}
	return &mfSourceReader{comObject{reader}}, nil
}

func getFrameSize(attrs *mfAttributes) (width, height uint32, err error) {
	val, err := attrs.getUINT64(&guidMFMtFrameSize)
	if err != nil {
		return 0, 0, err
	}
	width = uint32(val >> 32)
	height = uint32(val & 0xFFFFFFFF)
	return width, height, nil
}

func ListCameras() ([]CameraInfo, error) {
	if err := initMF(); err != nil {
		return nil, err
	}
	defer shutdownMF()
	attrs, err := createAttributes(1)
	if err != nil {
		return nil, err
	}
	defer attrs.release()
	if err := attrs.setGUID(&guidMFDevsourceAttributeSourceType, &guidMFDevsourceAttributeSourceTypeVidcapGuid); err != nil {
		return nil, err
	}
	devices, err := enumDeviceSources(attrs)
	if err != nil {
		return nil, err
	}
	if len(devices) == 0 {
		return nil, ErrNoCameras
	}
	cameras := make([]CameraInfo, len(devices))
	for i, dev := range devices {
		name, _ := dev.getAllocatedString(&guidMFDevsourceAttributeFriendlyName)
		symbolicLink, _ := dev.getAllocatedString(&guidMFDevsourceAttributeSourceTypeVidcapSymbolicLink)
		if name == "" {
			name = "Unknown Camera"
		}
		cameras[i] = CameraInfo{
			DeviceID: symbolicLink,
			Name:     name,
			Driver:   "Media Foundation",
		}
		dev.release()
	}
	return cameras, nil
}

// OpenCamera opens a camera device with the given configuration.
func OpenCamera(config CameraConfig) (Camera, error) {
	if err := initMF(); err != nil {
		return nil, err
	}
	attrs, err := createAttributes(1)
	if err != nil {
		shutdownMF()
		return nil, err
	}
	defer attrs.release()
	if err := attrs.setGUID(&guidMFDevsourceAttributeSourceType, &guidMFDevsourceAttributeSourceTypeVidcapGuid); err != nil {
		shutdownMF()
		return nil, err
	}
	devices, err := enumDeviceSources(attrs)
	if err != nil {
		shutdownMF()
		return nil, err
	}
	if len(devices) == 0 {
		shutdownMF()
		return nil, ErrCameraNotFound
	}
	deviceIndex := 0
	if config.DeviceID != "" {
		for i, dev := range devices {
			link, _ := dev.getAllocatedString(&guidMFDevsourceAttributeSourceTypeVidcapSymbolicLink)
			if link == config.DeviceID {
				deviceIndex = i
				break
			}
		}
	}
	deviceName, _ := devices[deviceIndex].getAllocatedString(&guidMFDevsourceAttributeFriendlyName)
	if deviceName == "" {
		deviceName = "Unknown Camera"
	}
	sourcePtr, err := devices[deviceIndex].activateObject(&guidIIDIMFMediaSource)
	if err != nil {
		for _, d := range devices {
			d.release()
		}
		shutdownMF()
		return nil, err
	}
	source := &mfMediaSource{comObject{sourcePtr}}
	for _, d := range devices {
		d.release()
	}
	reader, err := createSourceReaderFromMediaSource(source, nil)
	if err != nil {
		source.release()
		shutdownMF()
		return nil, err
	}
	preferredWidth := config.PreferredWidth
	preferredHeight := config.PreferredHeight
	if preferredWidth <= 0 {
		preferredWidth = 320
	}
	if preferredHeight <= 0 {
		preferredHeight = 240
	}
	var bestType *mfMediaType
	var bestWidth uint32
	var isMJPEG, isRGB bool
	// First pass: look for MJPEG
	for typeIndex := uint32(0); ; typeIndex++ {
		mediaType, err := reader.getNativeMediaType(mfSourceReaderFirstVideoStream, typeIndex)
		if err != nil {
			break
		}
		subtype, err := mediaType.getGUID(&guidMFMtSubtype)
		if err != nil {
			mediaType.release()
			continue
		}
		width, height, err := getFrameSize(&mediaType.mfAttributes)
		if err != nil {
			mediaType.release()
			continue
		}
		if subtype.equals(&guidMFVideoFormatMJPG) {
			if width >= uint32(preferredWidth) && height >= uint32(preferredHeight) {
				if bestWidth == 0 || width < bestWidth {
					if bestType != nil {
						bestType.release()
					}
					bestType = mediaType
					bestWidth = width
					isMJPEG = true
					isRGB = false
					continue
				}
			}
		}
		mediaType.release()
	}
	// Second pass: if no MJPEG, look for RGB
	if bestType == nil {
		for typeIndex := uint32(0); ; typeIndex++ {
			mediaType, err := reader.getNativeMediaType(mfSourceReaderFirstVideoStream, typeIndex)
			if err != nil {
				break
			}
			subtype, err := mediaType.getGUID(&guidMFMtSubtype)
			if err != nil {
				mediaType.release()
				continue
			}
			width, height, err := getFrameSize(&mediaType.mfAttributes)
			if err != nil {
				mediaType.release()
				continue
			}
			// TODO: YUV/NV12 support?
			isSupported := subtype.equals(&guidMFVideoFormatRGB24) ||
				subtype.equals(&guidMFVideoFormatRGB32)
			if isSupported && width >= uint32(preferredWidth) && height >= uint32(preferredHeight) {
				if bestWidth == 0 || width < bestWidth {
					if bestType != nil {
						bestType.release()
					}
					bestType = mediaType
					bestWidth = width
					isMJPEG = false
					isRGB = subtype.equals(&guidMFVideoFormatRGB24) || subtype.equals(&guidMFVideoFormatRGB32)
					continue
				}
			}
			mediaType.release()
		}
	}
	if bestType != nil {
		reader.setCurrentMediaType(mfSourceReaderFirstVideoStream, bestType)
		bestType.release()
	}
	currentType, err := reader.getCurrentMediaType(mfSourceReaderFirstVideoStream)
	if err != nil {
		reader.release()
		source.release()
		shutdownMF()
		return nil, err
	}
	finalWidth, finalHeight, _ := getFrameSize(&currentType.mfAttributes)
	subtype, _ := currentType.getGUID(&guidMFMtSubtype)
	if subtype != nil {
		isMJPEG = subtype.equals(&guidMFVideoFormatMJPG)
		isRGB = subtype.equals(&guidMFVideoFormatRGB24) || subtype.equals(&guidMFVideoFormatRGB32)
	}
	currentType.release()
	var stride uint32
	if isRGB {
		if subtype != nil && subtype.equals(&guidMFVideoFormatRGB24) {
			stride = finalWidth * 3
			stride = (stride + 3) &^ 3
		} else {
			stride = finalWidth * 4
		}
	}
	return &windowsCamera{
		reader:     reader,
		source:     source,
		deviceName: deviceName,
		width:      finalWidth,
		height:     finalHeight,
		stride:     stride,
		isMJPEG:    isMJPEG,
		isRGB:      isRGB,
	}, nil
}

func (c *windowsCamera) Start() error {
	return nil
}

func (c *windowsCamera) Stop() error {
	return nil
}

func (c *windowsCamera) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.reader != nil {
		c.reader.release()
		c.reader = nil
	}
	if c.source != nil {
		c.source.release()
		c.source = nil
	}
	shutdownMF()
	return nil
}

func (c *windowsCamera) GetFrame() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.reader == nil {
		return nil, errors.New("camera not open")
	}
	sample, _, _, _, err := c.reader.readSample(mfSourceReaderFirstVideoStream, 0)
	if err != nil {
		return nil, err
	}
	if sample == nil {
		return nil, ErrNoFrameAvailable
	}
	defer sample.release()
	buffer, err := sample.convertToContiguousBuffer()
	if err != nil {
		return nil, err
	}
	defer buffer.release()
	data, err := buffer.lock()
	if err != nil {
		return nil, err
	}
	defer buffer.unlock()

	if c.isMJPEG {
		result := make([]byte, len(data))
		copy(result, data)
		return mjpegToJFIF(result), nil
	}

	// TODO: more efficient conversion
	img := image.NewRGBA(image.Rect(0, 0, int(c.width), int(c.height)))
	if c.isRGB && c.stride > 0 {
		bytesPerPixel := int(c.stride) / int(c.width)
		for y := 0; y < int(c.height); y++ {
			srcY := int(c.height) - 1 - y
			for x := 0; x < int(c.width); x++ {
				srcIdx := srcY*int(c.stride) + x*bytesPerPixel
				if srcIdx+2 < len(data) {
					dstIdx := y*img.Stride + x*4
					img.Pix[dstIdx+0] = data[srcIdx+2]
					img.Pix[dstIdx+1] = data[srcIdx+1]
					img.Pix[dstIdx+2] = data[srcIdx+0]
					img.Pix[dstIdx+3] = 255
				}
			}
		}
	} else {
		return nil, errors.New("unsupported video format")
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		return nil, fmt.Errorf("failed to encode JPEG: %w", err)
	}
	return buf.Bytes(), nil
}

func (c *windowsCamera) Name() string {
	return c.deviceName
}
