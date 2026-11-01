//! Black-box scenario driver for the Rust rmtt client library.
//!
//! Prints machine-readable events and ends with `RESULT PASS` / `RESULT FAIL`.

use std::sync::mpsc;
use std::time::{Duration, Instant};

use rmtt::{Client, ClientOptions, TlsConfig};

fn arg(args: &[String], name: &str, default: &str) -> String {
    args.iter()
        .position(|a| a == name)
        .and_then(|i| args.get(i + 1))
        .cloned()
        .unwrap_or_else(|| default.to_string())
}

fn fail(msg: &str) -> ! {
    println!("RESULT FAIL {msg}");
    std::process::exit(1);
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let transport = arg(&args, "-transport", "tcp");
    let host = arg(&args, "-host", "127.0.0.1");
    let port = arg(&args, "-port", "19990");
    let credential = arg(&args, "-credential", "rust-dev");
    let keepalive: u16 = arg(&args, "-keepalive", "30").parse().unwrap_or(30);
    let scenario = arg(&args, "-scenario", "basic");
    let count: usize = arg(&args, "-count", "5").parse().unwrap_or(5);
    let size: usize = arg(&args, "-size", "262144").parse().unwrap_or(262144);
    let wait: u64 = arg(&args, "-wait", "15").parse().unwrap_or(15);
    let reconnect = args.iter().any(|a| a == "-reconnect");

    let addr = format!("{transport}://{host}:{port}");
    let mut opts = ClientOptions {
        credential: credential.clone(),
        heartbeat_seconds: keepalive,
        connect_timeout: Duration::from_secs(5),
        auto_reconnect: reconnect,
        reconnect_base: Duration::from_millis(200),
        reconnect_max: Duration::from_millis(1000),
        reconnect_jitter: 0.0,
        ..Default::default()
    };
    if transport == "tls" {
        opts.tls = Some(TlsConfig { server_name: Some(host.clone()), insecure: true });
    }

    let client = match Client::connect(&addr, opts) {
        Ok(c) => c,
        Err(e) => {
            if scenario == "authreject" {
                println!("EVENT CONNECT_REJECTED {e}");
                println!("RESULT PASS");
                return;
            }
            fail(&format!("connect failed: {e}"));
        }
    };
    println!("EVENT CONNECTED server_kp={}", client.server_keepalive());
    use std::io::Write as _;
    let _ = std::io::stdout().flush();

    let (tx, rx) = mpsc::channel::<Vec<u8>>();
    client.set_payload_handler(move |msg| {
        let _ = tx.send(msg.payload().to_vec());
    });

    let push = |payload: &[u8]| {
        if let Err(e) = client.push(payload) {
            fail(&format!("push failed: {e}"));
        }
        println!("EVENT SENT len={}", payload.len());
        use std::io::Write as _;
        let _ = std::io::stdout().flush();
    };
    let wait_echo = |want: &[u8]| {
        let deadline = Instant::now() + Duration::from_secs(6);
        loop {
            let left = deadline.saturating_duration_since(Instant::now());
            match rx.recv_timeout(left) {
                Ok(p) if p == want => {
                    println!("EVENT ECHO_OK len={}", p.len());
                    return;
                }
                Ok(p) => println!("EVENT ECHO_OTHER len={}", p.len()),
                Err(_) => fail("timeout waiting for echo"),
            }
        }
    };

    match scenario.as_str() {
        "basic" => {
            let msg = b"hello-from-rust";
            push(msg);
            wait_echo(&echo_for(msg));
            client.disconnect();
            println!("RESULT PASS");
        }
        "concurrent" => {
            for i in 0..count {
                push(format!("msg-{i}").as_bytes());
            }
            let mut got = std::collections::HashSet::new();
            let deadline = Instant::now() + Duration::from_secs(10);
            while got.len() < count && Instant::now() < deadline {
                match rx.recv_timeout(Duration::from_millis(500)) {
                    Ok(p) => {
                        got.insert(p);
                    }
                    Err(_) => {}
                }
            }
            for i in 0..count {
                if !got.contains(format!("echo:msg-{i}").as_bytes()) {
                    fail(&format!("missing echo msg-{i}"));
                }
            }
            println!("EVENT ECHO_OK count={count}");
            client.disconnect();
            println!("RESULT PASS");
        }
        "large" | "binary" => {
            let payload: Vec<u8> = if scenario == "large" {
                let mut v = Vec::with_capacity(size);
                while v.len() < size {
                    v.extend_from_slice(b"0123456789abcdef");
                }
                v.truncate(size);
                v
            } else {
                (0u8..=255).collect()
            };
            push(&payload);
            wait_echo(&echo_for(&payload));
            client.disconnect();
            println!("RESULT PASS");
        }
        "downlink" => match rx.recv_timeout(Duration::from_secs(wait)) {
            Ok(p) => {
                println!("EVENT RECV len={} payload={:?}", p.len(), String::from_utf8_lossy(&p[..p.len().min(80)]));
                println!("RESULT PASS");
            }
            Err(_) => fail("no downstream push"),
        },
        "hold" => {
            println!("EVENT READY");
            use std::io::Write as _;
            let _ = std::io::stdout().flush();
            std::thread::sleep(Duration::from_secs(wait));
            if !client.is_connected() {
                fail("connection lost while holding");
            }
            println!("EVENT STILL_CONNECTED");
            println!("RESULT PASS");
        }
        "kick" => {
            println!("EVENT READY");
            use std::io::Write as _;
            let _ = std::io::stdout().flush();
            let deadline = Instant::now() + Duration::from_secs(wait);
            while Instant::now() < deadline {
                if !client.is_connected() {
                    println!("EVENT DISCONNECTED");
                    println!("RESULT PASS");
                    return;
                }
                std::thread::sleep(Duration::from_millis(100));
            }
            fail("no disconnect observed");
        }
        "takeover-attacker" => {
            println!("EVENT READY");
            use std::io::Write as _;
            let _ = std::io::stdout().flush();
            std::thread::sleep(Duration::from_secs(wait));
            if !client.is_connected() {
                fail("attacker lost its session");
            }
            push(b"attacker-alive");
            wait_echo(&echo_for(b"attacker-alive"));
            client.disconnect();
            println!("RESULT PASS");
        }
        "keepalive" => {
            std::thread::sleep(Duration::from_secs(6));
            if !client.is_connected() {
                fail("connection reaped despite heartbeat");
            }
            push(b"after-pings");
            wait_echo(&echo_for(b"after-pings"));
            println!("EVENT KEEPALIVE_OK");
            client.disconnect();
            println!("RESULT PASS");
        }
        "reconnect" => {
            push(b"before");
            wait_echo(&echo_for(b"before"));
            println!("EVENT READY_FOR_KILL");
            use std::io::Write as _;
            let _ = std::io::stdout().flush();
            std::thread::sleep(Duration::from_secs(3));
            let deadline = Instant::now() + Duration::from_secs(25);
            loop {
                if Instant::now() > deadline {
                    fail("did not reconnect / echo after reconnect");
                }
                if client.push(b"after").is_ok() {
                    match rx.recv_timeout(Duration::from_millis(500)) {
                        Ok(p) if p == echo_for(b"after") => break,
                        _ => {}
                    }
                }
                std::thread::sleep(Duration::from_millis(200));
            }
            println!("EVENT RECONNECTED");
            client.disconnect();
            println!("RESULT PASS");
        }
        other => fail(&format!("unknown scenario {other}")),
    }
    // Give the IO thread a moment to flush the clean DISCONNECT(0x00).
    std::thread::sleep(Duration::from_millis(300));
}

fn echo_for(p: &[u8]) -> Vec<u8> {
    let mut v = b"echo:".to_vec();
    v.extend_from_slice(p);
    v
}
