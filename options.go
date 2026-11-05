package awdb

// Option 配置 Open。
type Option func(*options)

type options struct {
	preload bool
}

func newOptions(opts []Option) *options {
	cfg := &options{}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	return cfg
}

// WithPreload 把整个 .awdb 文件读进堆内存（os.ReadFile + OpenBytes），
// 而不是 mmap。
//
// 适合库文件固定、实例内存可控的部署：没有运行期缺页，RSS ≈ 文件大小，
// 也不占文件句柄；代价是启动时多一次完整读取，且不再与其他进程共享
// page cache。默认 false，即 mmap（unix）或 pread（其余情况）。
func WithPreload(preload bool) Option {
	return func(c *options) { c.preload = preload }
}
