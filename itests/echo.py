"""The echo service on every server of the live tests.

Answers "<host> <proto> <peer address>" on TCP and UDP port 7000 and TCP port 7001,
over IPv4 and IPv6. livelab.py copies it to the servers and runs it there as a script.
"""

import socket
import socketserver
import threading

HOST = socket.gethostname()


def peer(address):
    """Return the peer's IP address, IPv4 without its IPv6-mapped prefix."""
    return address[0].removeprefix("::ffff:")


class Tcp(socketserver.BaseRequestHandler):
    """Answer a TCP connection with one echo line."""

    def handle(self):
        """Send the echo line to the peer."""
        self.request.sendall(f"{HOST} tcp {peer(self.client_address)}\n".encode())


class Udp(socketserver.BaseRequestHandler):
    """Answer a UDP datagram with one echo line."""

    def handle(self):
        """Send the echo line back to the datagram's sender."""
        answer = f"{HOST} udp {peer(self.client_address)}\n".encode()
        self.request[1].sendto(answer, self.client_address)


class DualStackTcpServer(socketserver.ThreadingTCPServer):
    """A threading TCP server on one socket for IPv4 and IPv6."""

    address_family = socket.AF_INET6
    allow_reuse_address = True

    def server_bind(self):
        """Accept IPv4 too, then bind."""
        self.socket.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 0)
        super().server_bind()


class DualStackUdpServer(socketserver.UDPServer):
    """A UDP server on one socket for IPv4 and IPv6."""

    address_family = socket.AF_INET6

    def server_bind(self):
        """Accept IPv4 too, then bind."""
        self.socket.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 0)
        super().server_bind()


def main():
    """Serve TCP 7000 and 7001 in threads and UDP 7000 until killed."""
    for port in (7000, 7001):
        threading.Thread(
            target=DualStackTcpServer(("::", port), Tcp).serve_forever, daemon=True
        ).start()
    DualStackUdpServer(("::", 7000), Udp).serve_forever()


if __name__ == "__main__":
    main()
