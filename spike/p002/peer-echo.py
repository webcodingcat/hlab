"""Answer every TCP connection and UDP datagram with the peer address the guest sees."""
import socket
import threading


def bind(family, kind, host, port):
    # V6ONLY, so the IPv6 socket does not collide with the IPv4 one on the same port.
    server = socket.socket(family, kind)
    server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    if family == socket.AF_INET6:
        server.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 1)
    server.bind((host, port))
    return server


def tcp(family, host):
    server = bind(family, socket.SOCK_STREAM, host, 7000)
    server.listen()
    while True:
        conn, peer = server.accept()
        conn.sendall(f"tcp peer {peer[0]}\n".encode())
        conn.close()


def udp(family, host):
    server = bind(family, socket.SOCK_DGRAM, host, 7001)
    while True:
        _, peer = server.recvfrom(1024)
        server.sendto(f"udp peer {peer[0]}\n".encode(), peer)


for target, family, host in ((tcp, socket.AF_INET, "0.0.0.0"), (udp, socket.AF_INET, "0.0.0.0"),
                             (tcp, socket.AF_INET6, "::"), (udp, socket.AF_INET6, "::")):
    threading.Thread(target=target, args=(family, host), daemon=True).start()
threading.Event().wait()
