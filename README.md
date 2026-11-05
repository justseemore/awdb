# awdb-go

用 Go 重写的 AWDB 离线 IP 库解析器。

原包：[gitee.com/aiwen_home/awdb-php](https://gitee.com/aiwen_home/awdb-php)（ipplus360 官方 PHP 解析实现）。
本实现同时对齐了官方的 Python 版（`gitee.com/aiwen_home/awdb-python`），二进制偏置、节点约定与解码规则完全一致，
因此同一个 `.awdb` 文件在 PHP / Python / Go 三个实现下解析出的结果相同。

- 零第三方依赖，只用标准库
- 只读、并发安全、`unix` 下 mmap，树遍历零分配
- 类型化访问（`Str` / `Uint` / `Int` / `Float` / `Rows`）+ 保序 JSON
- 带越界校验、指针环深度保护，损坏文件返回明确错误而不是空结果

## 目录结构

```
awdb-go/
├── go.mod
├── awdb.go          Reader：Open/OpenBytes/NewReaderAt、Lookup、树遍历
├── metadata.go      JSON 头部解析（node_count / ip_version / decode_type / byte_len / columns）
├── decode.go        两种记录编码的解码（structure / direct）
├── record.go        Record：按列名取值、Map()、保序 MarshalJSON
├── errors.go        哨兵错误
├── options.go       Option（WithPreload）
├── file_unix.go     mmap 快路径（build tag: unix）
├── file_other.go    非 unix 平台退化为整文件读取
├── builder_test.go  测试用的 awdb 文件生成器（合成库，无需真实数据文件）
├── awdb_test.go     单元测试与基准
└── cmd/awdb/        命令行工具
```

## 快速开始

```bash
cd awdb-go
go build ./...
go test ./...
```

当作库使用：

```go
package main

import (
	"errors"
	"fmt"

	"github.com/justseemore/awdb"
)

func main() {
	// 整库常驻内存时用 awdb.Open(path, awdb.WithPreload(true))，默认是 mmap
	reader, err := awdb.Open("ip.awdb")
	if err != nil {
		panic(err)
	}
	defer reader.Close()

	record, err := reader.Lookup("114.114.114.114")
	if errors.Is(err, awdb.ErrNotFound) {
		fmt.Println("查无数据")
		return
	}
	if err != nil {
		panic(err)
	}

	fmt.Println(record.Str("country"), record.Str("province"), record.Str("city"))
	fmt.Println(record.String()) // {"continent":"亚洲","country":"中国",...}
}
```

模块路径是 `github.com/justseemore/awdb`，包名仍是 `awdb`（导入后照样写 `awdb.Open(...)`）。其它 Go 项目直接引入：

```bash
go get github.com/justseemore/awdb@latest
```

```go
import "github.com/justseemore/awdb"
```

仓库没打 tag 时 Go 会用伪版本（`v0.0.0-<时间>-<commit>`）；正式发版建议打语义化 tag：

```bash
git tag v0.1.0 && git push origin v0.1.0
go get github.com/justseemore/awdb@v0.1.0
```

还没推送、想先在本地联调，可以在业务项目的 `go.mod` 里用 `replace` 指向本地目录：

```
require github.com/justseemore/awdb v0.0.0

replace github.com/justseemore/awdb => "/Users/zhaopengfei/Library/Application Support/CherryStudio/Data/Workspace/awdb-go"
```

注意：`go.mod` 里的路径是按词法分词的，**路径含空格时必须用引号包起来**，否则 `go mod tidy` 会报语法错误。

### 出错时的行为

`Lookup` 返回 `(Record, error)`。未命中返回 `awdb.ErrNotFound`（继续用 `record` 也不会 panic，它是空记录，
`String()` 是 `{}`，和 PHP 版返回 `{}` 一致）；地址与库版本不匹配、文件损坏等情况会返回各自的具体错误，
全部可以用 `errors.Is` 判断。

### 命令行

```bash
go run ./cmd/awdb -db ip.awdb 8.8.8.8 114.114.114.114
go run ./cmd/awdb -db ip.awdb -pretty 8.8.8.8
cat ips.txt | go run ./cmd/awdb -db ip.awdb
go run ./cmd/awdb -db ip.awdb -meta        # 打印头部元信息
```

## PHP 版 API 对照

| PHP (`aw\ReadAwdb`) | Go |
| --- | --- |
| `new ReadAwdb($file)` | `awdb.Open(path)` / `awdb.OpenBytes(b)` / `awdb.NewReaderAt(ra, size)` |
| `readAwdb($ip)` | `Lookup(string)` / `LookupAddr(netip.Addr)` / `LookupBytes([]byte)` |
| `findIndex()` | `recordOffset()` + `findNode()` |
| `decodeContent()` | `decodeValue()` |
| `getPointer()` | `decodeValue()` 的 pointer 分支 |
| `decodeContentStructure()` | `decodeRecord()` 的 `DecodeStructure` 分支 |
| `decodeContentDirect()` | `decodeDirect()` |
| `mapKeyValue()` | `Metadata.record()` |
| `openFile()` | `region()` |
| `bigEndianToInt()` | `beUint()` |
| `close()` | `Close()` |
| `isIpv4/isIpv6/intToIpv4/intToIpv6/ipv4MapIpv6/isIPv4MappedIPv6` | `net/netip` + `normalize()` |

## 文件格式（本包实现的那一代）

```
+----------------+----------------------------------------------------------+
| 2 字节          | JSON 头长度 metadataLen（大端 uint16）                      |
+----------------+----------------------------------------------------------+
| metadataLen     | JSON：node_count / ip_version / decode_type / byte_len     |
|                 |       / columns                                          |
+----------------+----------------------------------------------------------+
| node_count *    | 检索树。每个节点两个分支（0/1），每分支 byte_len 字节大端      |
| 2 * byte_len    | 分支值 <= node_count 指向节点，== node_count 表示空，        |
|                 | > node_count 表示叶子                                      |
+----------------+----------------------------------------------------------+
| 剩余             | 数据区。叶子值 v 对应记录偏移 rel = v - node_count - 10，    |
|                 | 记录地址 = 数据区起始 + rel                                |
+----------------+----------------------------------------------------------+
```

查询流程：

1. 归一化地址：`4` 库只接受 IPv4（32 位树），`6` 库只接受 IPv6（128 位树），`4_6` 库两者都接；
   IPv4 在 `4_6` 库中按 `::ffff:a.b.c.d` 处理，从地址第 96 位、树的**节点 96** 开始走（见下文注意事项）。
2. 每个树层级读 1 个地址位，MSB 优先，取对应分支。
3. 命中 `node_count` 表示该网段没有记录；命中 `> node_count` 得到叶子。
4. 按 `node_count + 10` 换算出数据区偏移，解码记录。
5. 按 `columns` 把值映射成字段；最后一列若是字符串数组，则按「上一列是列表名、数组是行内字段名」的规则
   展开成嵌套记录（PHP 版 `mapKeyValue` 的同款规则）。

`decode_type = 1`（structure）时，值带类型标签：

| tag | 类型 | 编码 |
| --- | --- | --- |
| 1 | array | 1 字节元素个数 + 顺序排列的元素 |
| 2 | pointer | 1 字节长度 + 大端偏移（相对数据区起始，指向另一条记录） |
| 3 | string | 1 字节长度 + 字节 |
| 4 | long string | 1 字节长度字节数 + 大端长度 + 字节 |
| 5 | uint | 1 字节长度 + 大端无符号数 |
| 6 | int | 4 字节大端有符号 |
| 7 | float | 4 字节大端 float32（按 float64 返回，与 PHP 输出一致） |
| 8 | double | 8 字节大端 float64 |

`decode_type = 2`（direct）时，记录是「4 字节大端正文长度 + 制表符分隔的一行文本」，所有字段都是字符串
（与 PHP/Python 一致，不做数字推断）。

## 与原 PHP 版的差异

1. **不再需要 GMP**：PHP 版对 IPv6 十进制大整数用 `gmp_init(str_replace(",", "", number_format($ip)))`，
   而 `number_format()` 会走浮点，超大整数会丢精度。Go 版直接用 `net/netip` 处理 16 字节地址，只支持字符串与
   `netip.Addr` / `[]byte` 入口，不需要十进制大整数这条路径。
2. **错误处理**：PHP 版用 `echo`/空数组表示未命中与损坏；Go 版返回哨兵错误（`ErrNotFound`、`ErrCorrupt`、
   `ErrInvalidIP`、`ErrIPv6QueryOnIPv4DB`、`ErrIPv4QueryOnIPv6DB`、`ErrUnsupported`、`ErrClosed`）。
3. **并发安全**：PHP 版依赖 `fseek`/`ftell` 共享文件指针，一个实例不能被并发使用；Go 版只读 + 原子 `closed`，
   同一个 `Reader` 可被多 goroutine 同时查询（`Close` 除外）。
4. **性能**：`unix` 下整文件 mmap，树遍历只做切片索引（`region` 返回 `src` 的子切片，不分配）；
   无法 mmap 或使用 `NewReaderAt` 时退化为 `pread`，行为不变只是多了系统调用。
5. **防御**：所有读操作都有边界校验；structure 解码有 32 层深度上限，指针成环的文件返回 `ErrCorrupt`
   而不是无限递归。
6. **输入宽松度**：`4` 库现在接受 `::ffff:1.2.3.4` 这类 IPv4-mapped 输入（PHP 会报 "IPv6 queries" 错误），
   其余版本校验逻辑保持一致。
7. **输出**：`Record` 保留列顺序并实现了 `MarshalJSON`（与 PHP 关联数组 `json_encode` 的顺序一致），
   另外提供 `Map()` 转普通 map 以及 `Str/Uint/Int/Float/Rows` 类型化取值。

## 注意事项

- **`4_6` 库的 IPv4 分支从节点 96 开始**。这是参考实现（PHP 与 Python 完全相同）的约定：
  `node_index = 96` 同时作为起始位下标和起始节点下标。本包在 `normalize()` 里用常量 `ipv4BranchBit = 96`
  表达这一点；如果将来遇到构建约定不同的库，只需要改这里。
- **只支持「2 字节长度 + JSON 头」这一代 awdb**。以 `0xAB 0xCD 0xEF "ipplus360.com"` 作为尾部标记的
  MaxMind 风格变体不在本包范围内（那是另一个读取器，`record_size` 24/28/32 + 数据区 16 字节分隔符）。
- **`decode_type = 2` 的字段一律是字符串**，需要数值请自行转换，或使用 `decode_type = 1` 的库。
- **`Close` 不要与正在进行的查询并发**：`Close` 会解除 mmap，正在读的 goroutine 会访问已解除的映射。
- **三种内存形态**：默认 `unix` 下 mmap（映射全文件、物理页按访问增长，每库占 1 个 fd）；mmap 失败退化为
  `pread`（不缓存、每读一次系统调用）；`WithPreload(true)` 则整文件进堆（RSS ≈ 文件大小、0 fd）。
  `OpenBytes` 不拷贝切片，调用方不得再修改它。

## 测试

```bash
go test ./...
go test -race ./...
go vet ./...
go test -bench=. -benchmem
```

`builder_test.go` 会现场合成 awdb 文件，因此不依赖商业数据库，覆盖：

- `decode_type=2` 的 IPv4 库（/8、/24、/32 三种前缀）
- `decode_type=1` 的全部值类型（短/长字符串、uint、int、float、double、数组）
- 嵌套列表列（`multi_area` 风格）与保序 JSON 输出
- IPv6 库、`4_6` 库（含 `::ffff:` 输入）
- 版本不匹配、非法 IP、未命中、文件截断、头部非法、指针成环
- 文件 mmap 路径、`Close` 语义、并发查询

## 许可

与原包一致，Apache License 2.0。