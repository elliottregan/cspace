//! Minimal wire protocol between the client (TUI) and the daemon that owns
//! the actual PTYs. Deliberately dumb: a length-prefixed byte-relay, not a
//! structured state-sync protocol — the daemon does not parse ANSI or know
//! about vt100 at all, it just proxies raw bytes both directions. All
//! terminal emulation still happens client-side, exactly as in
//! mux-prototype; only PTY *ownership* moved to the daemon.
//!
//! Frame: `[u8 tag][u32 LE len][payload; len bytes]`

use std::io::{self, Read, Write};

pub const TAG_ATTACH: u8 = 0x01;
pub const TAG_INPUT: u8 = 0x02;
pub const TAG_RESIZE: u8 = 0x03;
pub const TAG_OUTPUT: u8 = 0x81;
pub const TAG_ATTACHED: u8 = 0x82;

pub fn write_frame<W: Write>(w: &mut W, tag: u8, payload: &[u8]) -> io::Result<()> {
    w.write_all(&[tag])?;
    w.write_all(&(payload.len() as u32).to_le_bytes())?;
    w.write_all(payload)?;
    w.flush()
}

pub struct Frame {
    pub tag: u8,
    pub payload: Vec<u8>,
}

/// Reads one frame, or `Ok(None)` on clean EOF (peer disconnected between
/// frames — the normal way a client detaches).
pub fn read_frame<R: Read>(r: &mut R) -> io::Result<Option<Frame>> {
    let mut tag = [0u8; 1];
    match r.read_exact(&mut tag) {
        Ok(()) => {}
        Err(e) if e.kind() == io::ErrorKind::UnexpectedEof => return Ok(None),
        Err(e) => return Err(e),
    }
    let mut len_buf = [0u8; 4];
    r.read_exact(&mut len_buf)?;
    let len = u32::from_le_bytes(len_buf) as usize;
    let mut payload = vec![0u8; len];
    r.read_exact(&mut payload)?;
    Ok(Some(Frame { tag: tag[0], payload }))
}

pub fn encode_resize(rows: u16, cols: u16) -> [u8; 4] {
    let mut buf = [0u8; 4];
    buf[0..2].copy_from_slice(&rows.to_le_bytes());
    buf[2..4].copy_from_slice(&cols.to_le_bytes());
    buf
}

pub fn decode_resize(payload: &[u8]) -> Option<(u16, u16)> {
    if payload.len() < 4 {
        return None;
    }
    let rows = u16::from_le_bytes([payload[0], payload[1]]);
    let cols = u16::from_le_bytes([payload[2], payload[3]]);
    Some((rows, cols))
}
