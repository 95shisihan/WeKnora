"""gRPC server over binary stdin/stdout, without a socket or proxy.

Uses hyper-h2 for HTTP/2 framing/flow control and existing grpc RpcMethodHandlers
for protobuf serialization and business dispatch. Supports unary and streaming
RPCs, cancellation, deadlines and trailers. stdout belongs exclusively to HTTP/2.
"""
import os
import queue
import re
import struct
import sys
import threading
import time
from collections import namedtuple
from concurrent.futures import ThreadPoolExecutor
from urllib.parse import quote

import grpc
from h2.config import H2Configuration
from h2.connection import H2Connection
from h2.events import RequestReceived, DataReceived, StreamEnded, StreamReset, ConnectionTerminated
from h2.exceptions import H2Error
from h2.settings import SettingCodes

MAX_MESSAGE = 4 << 20
Details = namedtuple("Details", "method invocation_metadata")


class _Abort(Exception):
    pass


class _Context:
    def __init__(self, server, stream_id, headers):
        self.server, self.stream_id = server, stream_id
        self.headers = headers
        self.incoming = queue.Queue(maxsize=4)
        self.buffer = bytearray()
        self.active = True
        self.sent_headers = False
        self.code, self.details = grpc.StatusCode.OK, ""
        self.callbacks = []
        self.lock = threading.RLock()
        self.deadline = None
        self.timer = None
        timeout = dict(headers).get("grpc-timeout", "")
        match = re.fullmatch(r"([0-9]{1,8})([HMSmun])", timeout)
        if match:
            seconds = int(match[1]) * {"H": 3600, "M": 60, "S": 1, "m": .001, "u": .000001, "n": .000000001}[match[2]]
            self.deadline = time.monotonic() + seconds

    def is_active(self):
        return self.active and (self.deadline is None or time.monotonic() < self.deadline)

    def time_remaining(self):
        return None if self.deadline is None else max(0, self.deadline - time.monotonic())

    def invocation_metadata(self):
        return tuple((k, v) for k, v in self.headers if not k.startswith(":") and k != "grpc-timeout")

    def peer(self):
        return "stdio:host"

    def add_callback(self, callback):
        with self.lock:
            if not self.active:
                return False
            self.callbacks.append(callback)
            return True

    def set_code(self, code):
        self.code = code

    def set_details(self, details):
        self.details = details

    def abort(self, code, details):
        self.code, self.details = code, details
        raise _Abort()

    def cancel(self):
        self.server._finish(self, grpc.StatusCode.CANCELLED, "cancelled")

    def _close(self):
        with self.lock:
            if not self.active:
                return
            self.active = False
            if self.timer:
                self.timer.cancel()
            callbacks, self.callbacks = self.callbacks, []
        try:
            self.incoming.put_nowait(None)
        except queue.Full:
            pass
        for callback in callbacks:
            try:
                callback()
            except Exception:
                pass

    def messages(self, deserialize):
        while self.is_active():
            try:
                data = self.incoming.get(timeout=.1)
            except queue.Empty:
                continue
            if data is None:
                return
            yield deserialize(data) if deserialize else data


class StdioServer:
    """Subset of grpc.Server registration/start APIs used by generated services.

    No compression, interceptors, TLS or socket listeners: this endpoint is a
    single host-owned local pipe. Register services before start().
    """
    def __init__(self, reader=None, writer=None, max_workers=16):
        self.reader = reader or sys.stdin.buffer
        self.writer = writer or sys.stdout.buffer
        self.handlers, self.registered = [], {}
        self.h2 = H2Connection(H2Configuration(client_side=False, header_encoding="utf-8"))
        self.condition = threading.Condition(threading.RLock())
        self.streams = {}
        self.executor = ThreadPoolExecutor(max_workers=max_workers)
        self.done = threading.Event()
        self.started = False

    def add_generic_rpc_handlers(self, handlers):
        if self.started:
            raise RuntimeError("register handlers before start")
        self.handlers.extend(handlers)

    def add_registered_method_handlers(self, service_name, handlers):
        if self.started:
            raise RuntimeError("register handlers before start")
        self.registered.update({f"/{service_name}/{name}": handler for name, handler in handlers.items()})

    def start(self):
        if self.started:
            raise RuntimeError("server already started")
        self.started = True
        if os.name == "nt":
            import msvcrt
            for stream in (self.reader, self.writer):
                msvcrt.setmode(stream.fileno(), os.O_BINARY)
        with self.condition:
            self.h2.initiate_connection()
            self.h2.update_settings({SettingCodes.MAX_CONCURRENT_STREAMS: 16, SettingCodes.MAX_HEADER_LIST_SIZE: 32768})
            self._flush()
        threading.Thread(target=self._read, daemon=True, name="grpc-stdio-reader").start()

    def wait_for_termination(self, timeout=None):
        return not self.done.wait(timeout)

    def stop(self, grace=None):
        self.done.set()
        with self.condition:
            contexts = list(self.streams.values())
            self.streams.clear()
            self.condition.notify_all()
        for context in contexts:
            context._close()
        self.executor.shutdown(wait=False, cancel_futures=True)
        return self.done

    def _flush(self):
        raw = self.h2.data_to_send()
        if raw:
            self.writer.write(raw)
            self.writer.flush()

    def _headers(self, context):
        if not context.sent_headers:
            self.h2.send_headers(context.stream_id, [(":status", "200"), ("content-type", "application/grpc"), ("grpc-accept-encoding", "identity")])
            context.sent_headers = True

    def _finish(self, context, code=None, details=None):
        with self.condition:
            if context.stream_id not in self.streams:
                return
            try:
                self._headers(context)
                status = code or context.code
                self.h2.send_headers(context.stream_id, [("grpc-status", str(status.value[0])),
                    ("grpc-message", quote(context.details if details is None else details, safe=""))], end_stream=True)
                self._flush()
            except (H2Error, OSError):
                pass
            self.streams.pop(context.stream_id, None)
            self.condition.notify_all()
        context._close()

    def _send(self, context, payload):
        if len(payload) > MAX_MESSAGE:
            context.abort(grpc.StatusCode.RESOURCE_EXHAUSTED, "response message exceeds 4 MiB")
        data = b"\0" + struct.pack(">I", len(payload)) + payload
        offset = 0
        with self.condition:
            while offset < len(data):
                if self.done.is_set() or not context.is_active() or context.stream_id not in self.streams:
                    raise _Abort()
                self._headers(context)
                available = min(self.h2.local_flow_control_window(context.stream_id), self.h2.max_outbound_frame_size, len(data) - offset)
                if available <= 0:
                    self.condition.wait(.1)
                    continue
                self.h2.send_data(context.stream_id, data[offset:offset + available])
                offset += available
                self._flush()

    def _dispatch(self, context, handler):
        try:
            if handler is None:
                context.abort(grpc.StatusCode.UNIMPLEMENTED, "unknown RPC")
            requests = context.messages(handler.request_deserializer)
            request = requests if handler.request_streaming else next(requests)
            if handler.request_streaming:
                result = (handler.stream_stream if handler.response_streaming else handler.stream_unary)(request, context)
            else:
                result = (handler.unary_stream if handler.response_streaming else handler.unary_unary)(request, context)
            for response in result if handler.response_streaming else [result]:
                if response is not None:
                    self._send(context, handler.response_serializer(response) if handler.response_serializer else response)
        except _Abort:
            pass
        except StopIteration:
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            context.set_details("missing request")
        except Exception:
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details("plugin handler failed")
        finally:
            self._finish(context)

    def _read(self):
        try:
            while not self.done.is_set():
                data = self.reader.read1(65536) if hasattr(self.reader, "read1") else os.read(self.reader.fileno(), 65536)
                if not data:
                    break
                with self.condition:
                    events = self.h2.receive_data(data)
                    for event in events:
                        if isinstance(event, RequestReceived):
                            headers = dict(event.headers)
                            context = _Context(self, event.stream_id, event.headers)
                            self.streams[event.stream_id] = context
                            if len(self.streams) > 16 or headers.get(":method") != "POST" or not headers.get("content-type", "").startswith("application/grpc"):
                                self._finish(context, grpc.StatusCode.RESOURCE_EXHAUSTED, "invalid request or stream limit")
                                continue
                            path = headers.get(":path", "")
                            handler = self.registered.get(path)
                            if handler is None:
                                for generic in self.handlers:
                                    handler = generic.service(Details(path, context.invocation_metadata()))
                                    if handler:
                                        break
                            if context.deadline is not None:
                                context.timer = threading.Timer(context.time_remaining(), self._finish, args=(context, grpc.StatusCode.DEADLINE_EXCEEDED, "deadline exceeded"))
                                context.timer.daemon = True
                                context.timer.start()
                            self.executor.submit(self._dispatch, context, handler)
                        elif isinstance(event, DataReceived):
                            context = self.streams.get(event.stream_id)
                            self.h2.acknowledge_received_data(event.flow_controlled_length, event.stream_id)
                            if context is None:
                                continue
                            context.buffer.extend(event.data)
                            while len(context.buffer) >= 5:
                                compressed, length = struct.unpack(">BI", context.buffer[:5])
                                if compressed or length > MAX_MESSAGE:
                                    self._finish(context, grpc.StatusCode.RESOURCE_EXHAUSTED, "compressed or oversized message")
                                    break
                                if len(context.buffer) < length + 5:
                                    break
                                payload = bytes(context.buffer[5:5 + length])
                                del context.buffer[:5 + length]
                                try:
                                    context.incoming.put_nowait(payload)
                                except queue.Full:
                                    self._finish(context, grpc.StatusCode.RESOURCE_EXHAUSTED, "request queue full")
                                    break
                        elif isinstance(event, StreamEnded):
                            context = self.streams.get(event.stream_id)
                            if context:
                                if context.buffer:
                                    self._finish(context, grpc.StatusCode.INVALID_ARGUMENT, "truncated message")
                                else:
                                    try:
                                        context.incoming.put_nowait(None)
                                    except queue.Full:
                                        self._finish(context, grpc.StatusCode.RESOURCE_EXHAUSTED, "request queue full")
                        elif isinstance(event, StreamReset):
                            context = self.streams.pop(event.stream_id, None)
                            if context:
                                context._close()
                        elif isinstance(event, ConnectionTerminated):
                            self.done.set()
                    self._flush()
                    self.condition.notify_all()
        except (H2Error, OSError, ValueError):
            pass
        finally:
            self.stop()
