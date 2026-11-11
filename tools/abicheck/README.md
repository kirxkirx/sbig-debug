# Windows ABI verification

Run `sh tools/abicheck/check.sh` with Go, Clang, curl, unzip, and sha256sum.
The script downloads the manufacturer's Windows SDK and checks its SHA-256
before extracting the header. Set `SBIG_SDK_ARCHIVE=/path/to/WinDevKit.zip`
to use an existing copy. The header and driver binaries are not redistributed.

Go generates C assertions from the layouts used by the live byte-buffer encoder
and decoder, command IDs, and single-word structures. Clang compiles the assertions
for 32-bit and 64-bit Windows using the vendor declarations. This catches layout
mistakes that a mock sharing the same assumptions could miss. It does not validate
camera behavior, DLL availability, or the installed driver's version.

Reference: [Diffraction Limited Windows SDK](https://diffractionlimited.com/downloads/sbig/WinDevKit.zip),
header dated 2019-04-17. The archive and extracted header hashes are pinned in
`check.sh`; a changed upstream archive requires an explicit review and hash update.
The small `Windows.h` stand-in supplies only a declaration used by the header;
it does not override Windows integer sizes or compiler struct alignment.
