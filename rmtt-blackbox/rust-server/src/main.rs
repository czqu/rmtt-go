//! Black-box test server for the Rust rmtt implementation.
//!
//! Wraps the real `rmtt::server::Server` (TCP) with CLI knobs and a localhost
//! line-based control socket so the external driver can exercise the
//! server-side API (push / kick / online / shutdown).

use std::collections::HashSet;
use std::io::{BufRead, BufReader, Write};
use std::net::{TcpListener, TcpStream};
use std::sync::Arc;
use std::thread;
use std::time::Duration;

use rmtt::server::{AuthResult, KeepalivePolicy, Server, ServerOptions};

fn arg(args: &[String], name: &str, default: &str) -> String {
    args.iter()
        .position(|a| a == name)
        .and_then(|i| args.get(i + 1))
        .cloned()
        .unwrap_or_else(|| default.to_string())
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let port = arg(&args, "-port", "19990");
    let control: u16 = arg(&args, "-control", "0").parse().unwrap_or(0);
    let min: u16 = arg(&args, "-keepalive-min", "30").parse().unwrap_or(30);
    let max: u16 = arg(&args, "-keepalive-max", "600").parse().unwrap_or(600);
    let default: u16 = arg(&args, "-keepalive-default", "60").parse().unwrap_or(60);
    let allow_disable = args.iter().any(|a| a == "-allow-disable");
    let no_echo = args.iter().any(|a| a == "-no-echo");
    let reject: HashSet<String> = arg(&args, "-reject", "")
        .split(',')
        .filter(|s| !s.is_empty())
        .map(|s| s.to_string())
        .collect();

    let opts = ServerOptions {
        keepalive: KeepalivePolicy {
            min_seconds: min,
            max_seconds: max,
            default_seconds: default,
            allow_disable,
        },
        ..Default::default()
    };

    let server = Arc::new(Server::bind(&format!("0.0.0.0:{port}"), opts).expect("bind failed"));
    let reject2 = reject.clone();
    server.set_authenticator(move |credential| {
        if reject2.contains(credential) {
            AuthResult::reject_with(3)
        } else {
            AuthResult::allow(credential)
        }
    });

    let s = server.clone();
    server.set_message_handler(move |device_id, payload| {
        println!(
            "PUSH device={} len={} payload={:?}",
            device_id,
            payload.len(),
            String::from_utf8_lossy(&payload[..payload.len().min(120)])
        );
        use std::io::Write as _;
        let _ = std::io::stdout().flush();
        if !no_echo {
            let mut echo = Vec::with_capacity(payload.len() + 5);
            echo.extend_from_slice(b"echo:");
            echo.extend_from_slice(payload);
            if let Err(e) = s.push(device_id, &echo) {
                eprintln!("echo push failed for {device_id}: {e}");
            }
        }
    });
    server.set_on_connect(|device_id| {
        println!("CONNECTED device={device_id}");
        use std::io::Write as _;
        let _ = std::io::stdout().flush();
    });
    server.set_on_disconnect(|device_id, reason| {
        println!("DISCONNECTED device={device_id} reason={reason}");
        use std::io::Write as _;
        let _ = std::io::stdout().flush();
    });

    if control > 0 {
        let ctl = server.clone();
        thread::spawn(move || control_loop(control, ctl));
    }

    println!("READY transport=tcp port={port} control={control} pid={}", std::process::id());
    use std::io::Write as _;
    let _ = std::io::stdout().flush();
    loop {
        thread::sleep(Duration::from_secs(60));
    }
}

fn control_loop(port: u16, server: Arc<Server>) {
    let listener = TcpListener::bind(("127.0.0.1", port)).expect("control bind");
    println!("CONTROL_READY port={port}");
    use std::io::Write as _;
    let _ = std::io::stdout().flush();
    for stream in listener.incoming() {
        let Ok(stream) = stream else { continue };
        let s = server.clone();
        thread::spawn(move || handle_control(stream, s));
    }
}

fn handle_control(stream: TcpStream, server: Arc<Server>) {
    let mut writer = stream.try_clone().expect("clone");
    let reader = BufReader::new(stream);
    for line in reader.lines() {
        let Ok(line) = line else { return };
        let line = line.trim().to_string();
        if line.is_empty() {
            continue;
        }
        println!("CONTROL {line}");
        use std::io::Write as _;
        let _ = std::io::stdout().flush();
        let parts: Vec<&str> = line.splitn(3, ' ').collect();
        let reply = match parts[0] {
            "PING" => "OK PONG".to_string(),
            "PUSH" if parts.len() == 3 => match server.push(parts[1], parts[2].as_bytes()) {
                Ok(()) => "OK".to_string(),
                Err(e) => format!("ERR {e}"),
            },
            "KICK" if parts.len() == 3 => {
                let code: u8 = parts[2].parse().unwrap_or(6);
                match server.kick(parts[1], code) {
                    Ok(()) => "OK".to_string(),
                    Err(e) => format!("ERR {e}"),
                }
            }
            "ONLINE" if parts.len() >= 2 => format!("OK {}", server.is_online(parts[1])),
            "COUNT" => format!("OK {}", server.online_count()),
            "SHUTDOWN" => {
                let _ = writeln!(writer, "OK");
                let _ = writer.flush();
                server.shutdown();
                std::process::exit(0);
            }
            _ => "ERR unknown".to_string(),
        };
        let _ = writeln!(writer, "{reply}");
        let _ = writer.flush();
    }
}
