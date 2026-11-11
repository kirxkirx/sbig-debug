/* Only the LARGE_INTEGER declaration used by the vendor header is needed for
 * these compile-time checks. No Windows implementation or driver is linked. */
#pragma once
typedef union _LARGE_INTEGER {
    struct { unsigned long LowPart; long HighPart; } u;
    long long QuadPart;
} LARGE_INTEGER;
