use std::io::{Read, Write};
use std::net::TcpStream;
use std::time::Duration;

/// Preserve mode deliberately makes no stop/shutdown request. The shell exits,
/// but sidecar keeps ownership of the live projects until the user stops them.
pub fn prepare_exit(port: &str, keep_projects: bool) -> Result<(), String> {
    if keep_projects {
        return Ok(());
    }
    let port: u16 = port.parse().map_err(|_| "后台端口无效".to_string())?;
    let addr = ([127, 0, 0, 1], port).into();
    let mut stream = TcpStream::connect_timeout(&addr, Duration::from_secs(3))
        .map_err(|e| format!("无法连接后台：{e}"))?;
    stream.set_read_timeout(Some(Duration::from_secs(30))).map_err(|e| e.to_string())?;
    stream.set_write_timeout(Some(Duration::from_secs(5))).map_err(|e| e.to_string())?;
    stream.write_all(b"POST /api/desktop/shutdown HTTP/1.0\r\nHost: localhost\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
        .map_err(|e| e.to_string())?;
    let mut response = String::new();
    stream.take(8192).read_to_string(&mut response).map_err(|e| format!("未确认后台退出：{e}"))?;
    let (headers, body) = response.split_once("\r\n\r\n").ok_or("后台响应无效")?;
    let status = headers.lines().next().and_then(|line| line.split_whitespace().nth(1));
    if status == Some("404") {
        return Err("后台版本过旧，请更新后再退出；项目尚未停止".to_string());
    }
    if status != Some("200") || !body.contains("\"shuttingDown\":true") {
        return Err("后台未确认退出，请等待启停操作完成后重试".to_string());
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::net::TcpListener;
    #[test]
    fn preserve_does_not_contact_or_stop_backend() {
        assert!(prepare_exit("not-a-port", true).is_ok());
    }
    fn respond(status: &str, body: &str) -> Result<(), String> {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let port = listener.local_addr().unwrap().port().to_string();
        let response = format!("HTTP/1.0 {status}\r\nConnection: close\r\n\r\n{body}");
        let server = std::thread::spawn(move || {
            let (mut stream, _) = listener.accept().unwrap();
            let mut request = [0; 1024];
            let n = stream.read(&mut request).unwrap();
            assert!(String::from_utf8_lossy(&request[..n]).starts_with("POST /api/desktop/shutdown "));
            stream.write_all(response.as_bytes()).unwrap();
        });
        let result = prepare_exit(&port, false);
        server.join().unwrap();
        result
    }
    #[test]
    fn stop_requires_successful_shutdown_acknowledgement() {
        assert!(respond("200 OK", r#"{"shuttingDown":true,"stopped":2}"#).is_ok());
        assert!(respond("409 Conflict", r#"{"error":"busy"}"#).is_err());
        assert!(respond("200 OK", r#"{"stopped":2}"#).is_err());
        assert!(respond("404 Not Found", "missing").unwrap_err().contains("版本过旧"));
    }
}
