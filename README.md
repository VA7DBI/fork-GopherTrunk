# GopherTrunk
A headless, low-latency trunking engine that manages hardware pools and decodes P25/DMR control channels.
GopherTrunk 📻🐹
GopherTrunk is a high-performance, concurrent digital trunking scanner engine written in Go. It leverages the power of Go's goroutines to manage multiple RTL-SDR dongles simultaneously, allowing for seamless tracking of P25, DMR, and NXDN trunked radio systems.
✨ Features
Native Concurrency: Designed from the ground up to use Go channels for low-latency IQ data processing.
Multi-SDR Pool: Auto-discovery and role assignment for multiple RTL-SDR devices (Control vs. Voice).
Protocol Support (Planned):
P25 Phase 1 & 2 (C4FM/QPSK)
DMR (Tier II & III)
NXDN (4800/9600 baud)
Headless Architecture: Core engine runs as a daemon with a gRPC/Websocket API for frontend integration.
Priority Tracking: Intelligent talkgroup preemption based on user-defined priority levels.
🛠 Tech Stack
Language: Go (Golang)
Hardware Interface: libusb / CGO wrappers for RTL-SDR
DSP: Custom Go-based polyphase channelizers and demodulators
API: gRPC & Protobuf for metadata and audio streaming
