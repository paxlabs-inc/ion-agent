use std::env;
use std::fs;
use std::io::{self, Read, Write};
use std::os::unix::fs::{FileTypeExt, PermissionsExt};
use std::os::unix::net::{UnixListener, UnixStream};
use std::path::{Path, PathBuf};
use std::sync::{mpsc, Arc};
use std::thread;

use usearch::{Index, IndexOptions, MetricKind, ScalarKind};

const VERSION: u8 = 1;
const RESPONSE_BIT: u8 = 0x80;
const STATUS_OK: u8 = 0;
const STATUS_ERROR: u8 = 1;
const OP_INSERT: u8 = 0x01;
const OP_SEARCH: u8 = 0x02;
const OP_DELETE: u8 = 0x03;
const OP_COUNT: u8 = 0x04;
const OP_RESET: u8 = 0x05;
const MAX_FRAME_SIZE: usize = 64 << 20;
const DEFAULT_CAPACITY: usize = 10_000;
const COMMAND_QUEUE_CAPACITY: usize = 256;

struct Config {
    socket_path: PathBuf,
    dimensions: usize,
    capacity: usize,
}

struct Service {
    commands: mpsc::SyncSender<Command>,
    dimensions: usize,
}

enum Command {
    Insert {
        key: u64,
        vector: Vec<f32>,
        result: mpsc::Sender<Result<(), String>>,
    },
    Search {
        vector: Vec<f32>,
        k: usize,
        result: mpsc::Sender<Result<Vec<(u64, f32)>, String>>,
    },
    Delete {
        key: u64,
        result: mpsc::Sender<Result<bool, String>>,
    },
    Count {
        result: mpsc::Sender<Result<usize, String>>,
    },
    Reset {
        result: mpsc::Sender<Result<(), String>>,
    },
}

impl Service {
    fn new(dimensions: usize, capacity: usize) -> Result<Self, String> {
        if dimensions == 0 {
            return Err("dimensions must be positive".into());
        }
        let (commands, receiver) = mpsc::sync_channel(COMMAND_QUEUE_CAPACITY);
        let (initialized, initialization) = mpsc::sync_channel(1);
        thread::Builder::new()
            .name("ion-hnsw-index".into())
            .spawn(move || match create_index(dimensions, capacity) {
                Ok(index) => {
                    let _ = initialized.send(Ok(()));
                    index_worker(index, capacity, receiver);
                }
                Err(err) => {
                    let _ = initialized.send(Err(err));
                }
            })
            .map_err(|err| format!("start index worker: {err}"))?;
        initialization
            .recv()
            .map_err(|_| "index worker stopped during initialization".to_string())??;
        Ok(Self {
            commands,
            dimensions,
        })
    }

    fn insert(&self, key: u64, vector: &[f32]) -> Result<(), String> {
        self.validate_vector(vector)?;
        let (result, response) = mpsc::channel();
        self.send(Command::Insert {
            key,
            vector: vector.to_vec(),
            result,
        })?;
        receive(response)
    }

    fn search(&self, vector: &[f32], k: usize) -> Result<Vec<(u64, f32)>, String> {
        self.validate_vector(vector)?;
        if k == 0 {
            return Ok(Vec::new());
        }
        let (result, response) = mpsc::channel();
        self.send(Command::Search {
            vector: vector.to_vec(),
            k,
            result,
        })?;
        receive(response)
    }

    fn delete(&self, key: u64) -> Result<bool, String> {
        let (result, response) = mpsc::channel();
        self.send(Command::Delete { key, result })?;
        receive(response)
    }

    fn count(&self) -> Result<usize, String> {
        let (result, response) = mpsc::channel();
        self.send(Command::Count { result })?;
        receive(response)
    }

    fn reset(&self) -> Result<(), String> {
        let (result, response) = mpsc::channel();
        self.send(Command::Reset { result })?;
        receive(response)
    }

    fn validate_vector(&self, vector: &[f32]) -> Result<(), String> {
        if vector.len() != self.dimensions {
            return Err(format!(
                "dimension mismatch: got {}, want {}",
                vector.len(),
                self.dimensions
            ));
        }
        let mut norm = 0.0_f64;
        for value in vector {
            if !value.is_finite() {
                return Err("vector contains a non-finite value".into());
            }
            norm += f64::from(*value) * f64::from(*value);
        }
        if norm == 0.0 {
            return Err("vector magnitude must be non-zero".into());
        }
        Ok(())
    }

    fn send(&self, command: Command) -> Result<(), String> {
        self.commands
            .send(command)
            .map_err(|_| "index worker stopped".to_string())
    }
}

fn create_index(dimensions: usize, capacity: usize) -> Result<Index, String> {
    let options = IndexOptions {
        dimensions,
        metric: MetricKind::Cos,
        quantization: ScalarKind::F32,
        connectivity: 16,
        expansion_add: 128,
        expansion_search: 64,
        multi: false,
    };
    let index = Index::new(&options).map_err(|err| err.to_string())?;
    index
        .reserve(capacity)
        .map_err(|err| format!("reserve index: {err}"))?;
    Ok(index)
}

fn index_worker(index: Index, initial_capacity: usize, commands: mpsc::Receiver<Command>) {
    for command in commands {
        match command {
            Command::Insert {
                key,
                vector,
                result,
            } => {
                let operation = (|| {
                    if index.contains(key) {
                        index
                            .remove(key)
                            .map_err(|err| format!("remove prior vector: {err}"))?;
                    }
                    if index.size() == index.capacity() {
                        let capacity = index
                            .capacity()
                            .saturating_mul(2)
                            .max(index.size().saturating_add(1))
                            .max(initial_capacity);
                        index
                            .reserve(capacity)
                            .map_err(|err| format!("grow index: {err}"))?;
                    }
                    index
                        .add(key, &vector)
                        .map_err(|err| format!("add vector: {err}"))
                })();
                let _ = result.send(operation);
            }
            Command::Search { vector, k, result } => {
                let operation = index
                    .search(&vector, k.min(index.size()))
                    .map(|matches| matches.keys.into_iter().zip(matches.distances).collect())
                    .map_err(|err| format!("search index: {err}"));
                let _ = result.send(operation);
            }
            Command::Delete { key, result } => {
                let operation = index
                    .remove(key)
                    .map(|removed| removed != 0)
                    .map_err(|err| format!("remove vector: {err}"));
                let _ = result.send(operation);
            }
            Command::Count { result } => {
                let _ = result.send(Ok(index.size()));
            }
            Command::Reset { result } => {
                let operation = index
                    .reset()
                    .map_err(|err| format!("reset index: {err}"))
                    .and_then(|()| {
                        index
                            .reserve(initial_capacity)
                            .map_err(|err| format!("reserve reset index: {err}"))
                    });
                let _ = result.send(operation);
            }
        }
    }
}

fn receive<T>(response: mpsc::Receiver<Result<T, String>>) -> Result<T, String> {
    response
        .recv()
        .map_err(|_| "index worker stopped before responding".to_string())?
}

fn main() {
    if let Err(err) = run() {
        eprintln!("ion-hnsw: {err}");
        std::process::exit(1);
    }
}

fn run() -> Result<(), String> {
    let config = parse_args(env::args().skip(1))?;
    if let Some(parent) = config.socket_path.parent() {
        fs::create_dir_all(parent).map_err(|err| format!("create socket directory: {err}"))?;
    }
    remove_stale_socket(&config.socket_path)?;

    let listener = UnixListener::bind(&config.socket_path)
        .map_err(|err| format!("bind {}: {err}", config.socket_path.display()))?;
    fs::set_permissions(&config.socket_path, fs::Permissions::from_mode(0o600))
        .map_err(|err| format!("secure socket permissions: {err}"))?;
    let _socket_guard = SocketGuard(config.socket_path.clone());
    let service = Arc::new(Service::new(config.dimensions, config.capacity)?);

    for connection in listener.incoming() {
        match connection {
            Ok(stream) => {
                let service = Arc::clone(&service);
                thread::spawn(move || {
                    if let Err(err) = serve_connection(stream, service) {
                        eprintln!("ion-hnsw: connection: {err}");
                    }
                });
            }
            Err(err) if err.kind() == io::ErrorKind::Interrupted => continue,
            Err(err) => return Err(format!("accept connection: {err}")),
        }
    }
    Ok(())
}

fn parse_args(arguments: impl Iterator<Item = String>) -> Result<Config, String> {
    let mut socket_path = None;
    let mut dimensions = None;
    let mut capacity = DEFAULT_CAPACITY;
    let arguments: Vec<String> = arguments.collect();
    let mut index = 0;
    while index < arguments.len() {
        let flag = &arguments[index];
        if flag == "--help" || flag == "-h" {
            return Err(usage());
        }
        index += 1;
        let value = arguments
            .get(index)
            .ok_or_else(|| format!("missing value for {flag}\n{}", usage()))?;
        match flag.as_str() {
            "--socket" => socket_path = Some(PathBuf::from(value)),
            "--dimensions" => {
                dimensions = Some(parse_positive(value, "dimensions")?);
            }
            "--capacity" => capacity = parse_positive(value, "capacity")?,
            _ => return Err(format!("unknown argument {flag}\n{}", usage())),
        }
        index += 1;
    }
    Ok(Config {
        socket_path: socket_path.ok_or_else(|| format!("--socket is required\n{}", usage()))?,
        dimensions: dimensions.ok_or_else(|| format!("--dimensions is required\n{}", usage()))?,
        capacity,
    })
}

fn parse_positive(value: &str, name: &str) -> Result<usize, String> {
    value
        .parse::<usize>()
        .map_err(|_| format!("{name} must be a positive integer"))
        .and_then(|parsed| {
            if parsed == 0 {
                Err(format!("{name} must be positive"))
            } else {
                Ok(parsed)
            }
        })
}

fn usage() -> String {
    "Usage: ion-hnsw --socket PATH --dimensions N [--capacity N]".into()
}

fn remove_stale_socket(path: &Path) -> Result<(), String> {
    match fs::symlink_metadata(path) {
        Ok(metadata) => {
            if !metadata.file_type().is_socket() {
                return Err(format!(
                    "refusing to remove non-socket path {}",
                    path.display()
                ));
            }
            fs::remove_file(path).map_err(|err| format!("remove stale socket: {err}"))
        }
        Err(err) if err.kind() == io::ErrorKind::NotFound => Ok(()),
        Err(err) => Err(format!("inspect socket path: {err}")),
    }
}

fn serve_connection(mut stream: UnixStream, service: Arc<Service>) -> io::Result<()> {
    loop {
        let body = match read_frame(&mut stream)? {
            Some(body) => body,
            None => return Ok(()),
        };
        let operation = body.get(1).copied().unwrap_or(0);
        let response = match handle_request(&service, &body) {
            Ok(payload) => success_response(operation, payload),
            Err(err) => error_response(operation, &err),
        };
        write_frame(&mut stream, &response)?;
    }
}

fn handle_request(service: &Service, body: &[u8]) -> Result<Vec<u8>, String> {
    let mut cursor = Cursor::new(body);
    let version = cursor.u8()?;
    if version != VERSION {
        return Err(format!("unsupported protocol version {version}"));
    }
    let operation = cursor.u8()?;
    let response = match operation {
        OP_INSERT => {
            let key = cursor.u64()?;
            let vector = cursor.vector()?;
            cursor.finish()?;
            service.insert(key, &vector)?;
            Vec::new()
        }
        OP_SEARCH => {
            let k = cursor.u32()? as usize;
            let vector = cursor.vector()?;
            cursor.finish()?;
            let matches = service.search(&vector, k)?;
            let count = u32::try_from(matches.len()).map_err(|_| "too many matches")?;
            let mut response = Vec::with_capacity(4 + matches.len() * 12);
            response.extend_from_slice(&count.to_be_bytes());
            for (key, distance) in matches {
                response.extend_from_slice(&key.to_be_bytes());
                response.extend_from_slice(&distance.to_bits().to_be_bytes());
            }
            response
        }
        OP_DELETE => {
            let key = cursor.u64()?;
            cursor.finish()?;
            vec![u8::from(service.delete(key)?)]
        }
        OP_COUNT => {
            cursor.finish()?;
            let count = u64::try_from(service.count()?).map_err(|_| "count overflow")?;
            count.to_be_bytes().to_vec()
        }
        OP_RESET => {
            cursor.finish()?;
            service.reset()?;
            Vec::new()
        }
        _ => return Err(format!("unknown operation {operation:#04x}")),
    };
    Ok(response)
}

fn success_response(operation: u8, payload: Vec<u8>) -> Vec<u8> {
    let mut response = Vec::with_capacity(3 + payload.len());
    response.extend_from_slice(&[VERSION, operation | RESPONSE_BIT, STATUS_OK]);
    response.extend_from_slice(&payload);
    response
}

fn error_response(operation: u8, message: &str) -> Vec<u8> {
    let bytes = message.as_bytes();
    let length = bytes.len().min(u16::MAX as usize);
    let mut response = Vec::with_capacity(5 + length);
    response.extend_from_slice(&[VERSION, operation | RESPONSE_BIT, STATUS_ERROR]);
    response.extend_from_slice(&(length as u16).to_be_bytes());
    response.extend_from_slice(&bytes[..length]);
    response
}

fn read_frame(reader: &mut impl Read) -> io::Result<Option<Vec<u8>>> {
    let mut length = [0_u8; 4];
    match reader.read_exact(&mut length) {
        Ok(()) => {}
        Err(err) if err.kind() == io::ErrorKind::UnexpectedEof => return Ok(None),
        Err(err) => return Err(err),
    }
    let length = u32::from_be_bytes(length) as usize;
    if length == 0 || length > MAX_FRAME_SIZE {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "invalid frame length",
        ));
    }
    let mut body = vec![0_u8; length];
    reader.read_exact(&mut body)?;
    Ok(Some(body))
}

fn write_frame(writer: &mut impl Write, body: &[u8]) -> io::Result<()> {
    let length = u32::try_from(body.len())
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidInput, "frame too large"))?;
    writer.write_all(&length.to_be_bytes())?;
    writer.write_all(body)?;
    writer.flush()
}

struct Cursor<'a> {
    bytes: &'a [u8],
    offset: usize,
}

impl<'a> Cursor<'a> {
    fn new(bytes: &'a [u8]) -> Self {
        Self { bytes, offset: 0 }
    }

    fn take(&mut self, length: usize) -> Result<&'a [u8], String> {
        let end = self
            .offset
            .checked_add(length)
            .ok_or_else(|| "request length overflow".to_string())?;
        let value = self
            .bytes
            .get(self.offset..end)
            .ok_or_else(|| "truncated request".to_string())?;
        self.offset = end;
        Ok(value)
    }

    fn u8(&mut self) -> Result<u8, String> {
        Ok(self.take(1)?[0])
    }

    fn u32(&mut self) -> Result<u32, String> {
        Ok(u32::from_be_bytes(
            self.take(4)?
                .try_into()
                .map_err(|_| "invalid u32".to_string())?,
        ))
    }

    fn u64(&mut self) -> Result<u64, String> {
        Ok(u64::from_be_bytes(
            self.take(8)?
                .try_into()
                .map_err(|_| "invalid u64".to_string())?,
        ))
    }

    fn vector(&mut self) -> Result<Vec<f32>, String> {
        let dimensions = self.u32()? as usize;
        let byte_length = dimensions
            .checked_mul(4)
            .ok_or_else(|| "vector length overflow".to_string())?;
        let bytes = self.take(byte_length)?;
        Ok(bytes
            .chunks_exact(4)
            .map(|chunk| {
                f32::from_bits(u32::from_be_bytes(
                    chunk.try_into().expect("four-byte chunk"),
                ))
            })
            .collect())
    }

    fn finish(&self) -> Result<(), String> {
        if self.offset == self.bytes.len() {
            Ok(())
        } else {
            Err("trailing request bytes".into())
        }
    }
}

struct SocketGuard(PathBuf);

impl Drop for SocketGuard {
    fn drop(&mut self) {
        let _ = fs::remove_file(&self.0);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn insert_search_delete_and_count_protocol_round_trip() {
        let service = Service::new(4, 8).unwrap();
        let mut insert = vec![VERSION, OP_INSERT];
        insert.extend_from_slice(&42_u64.to_be_bytes());
        insert.extend_from_slice(&4_u32.to_be_bytes());
        for value in [1.0_f32, 0.0, 0.0, 0.0] {
            insert.extend_from_slice(&value.to_bits().to_be_bytes());
        }
        assert!(handle_request(&service, &insert).unwrap().is_empty());
        assert_eq!(
            handle_request(&service, &[VERSION, OP_COUNT]).unwrap(),
            1_u64.to_be_bytes()
        );

        let mut search = vec![VERSION, OP_SEARCH];
        search.extend_from_slice(&10_u32.to_be_bytes());
        search.extend_from_slice(&4_u32.to_be_bytes());
        for value in [1.0_f32, 0.0, 0.0, 0.0] {
            search.extend_from_slice(&value.to_bits().to_be_bytes());
        }
        let response = handle_request(&service, &search).unwrap();
        assert_eq!(u32::from_be_bytes(response[0..4].try_into().unwrap()), 1);
        assert_eq!(u64::from_be_bytes(response[4..12].try_into().unwrap()), 42);

        let mut delete = vec![VERSION, OP_DELETE];
        delete.extend_from_slice(&42_u64.to_be_bytes());
        assert_eq!(handle_request(&service, &delete).unwrap(), vec![1]);
        assert_eq!(handle_request(&service, &delete).unwrap(), vec![0]);
    }

    #[test]
    fn ten_thousand_vectors_return_the_exact_query_in_top_ten() {
        let dimensions = 32;
        let service = Service::new(dimensions, 10_000).unwrap();
        let target = 7_777_u64;
        let mut query = Vec::new();
        for key in 1..=10_000_u64 {
            let vector = deterministic_vector(key, dimensions);
            if key == target {
                query.clone_from(&vector);
            }
            service.insert(key, &vector).unwrap();
        }
        let matches = service.search(&query, 10).unwrap();
        assert_eq!(matches.len(), 10);
        assert_eq!(matches[0].0, target);
        assert!(matches[0].1 <= 1e-5, "distance was {}", matches[0].1);
    }

    #[test]
    fn malformed_vectors_are_rejected_without_mutating_the_index() {
        let service = Service::new(2, 2).unwrap();
        assert!(service.insert(1, &[0.0, 0.0]).is_err());
        assert!(service.insert(1, &[f32::NAN, 1.0]).is_err());
        assert!(service.insert(1, &[1.0]).is_err());
        assert_eq!(service.count().unwrap(), 0);
    }

    fn deterministic_vector(key: u64, dimensions: usize) -> Vec<f32> {
        let mut state = key ^ 0x9e37_79b9_7f4a_7c15;
        let mut vector = Vec::with_capacity(dimensions);
        let mut norm = 0.0_f64;
        for _ in 0..dimensions {
            state ^= state << 13;
            state ^= state >> 7;
            state ^= state << 17;
            let value = ((state >> 40) as i32 - (1 << 23)) as f32;
            vector.push(value);
            norm += f64::from(value) * f64::from(value);
        }
        let norm = norm.sqrt() as f32;
        for value in &mut vector {
            *value /= norm;
        }
        vector
    }
}
