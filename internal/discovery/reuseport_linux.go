package discovery

// soReusePort is SO_REUSEPORT on Linux: the syscall package does not define it there.
const soReusePort = 0xf
