# Ion HNSW binary protocol

The service accepts persistent stream connections on a Unix domain socket.
Every message is one frame:

```
[body length: u32 big-endian] [body]
```

The maximum body length is 64 MiB. Request bodies begin with:

```
[version: u8 = 1] [operation: u8] [operation payload]
```

Responses begin with:

```
[version: u8 = 1] [operation | 0x80: u8] [status: u8] [response payload]
```

Status `0` is success. Status `1` is an error followed by a `u16` big-endian
UTF-8 error length and the error bytes.

All integers and IEEE-754 `f32` values are big-endian.

| Operation | Code | Request payload | Success payload |
| --- | ---: | --- | --- |
| Insert/upsert | `0x01` | `key:u64 dimensions:u32 vector:[f32; dimensions]` | empty |
| Search | `0x02` | `k:u32 dimensions:u32 vector:[f32; dimensions]` | `count:u32 (key:u64 distance:f32)*` |
| Delete | `0x03` | `key:u64` | `removed:u8` |
| Count | `0x04` | empty | `count:u64` |
| Reset | `0x05` | empty | empty |

Insert and search vectors must match the service dimension, contain only
finite values, and have non-zero magnitude. Unknown versions, operations,
trailing bytes, and oversized frames are rejected without terminating the
listener.
