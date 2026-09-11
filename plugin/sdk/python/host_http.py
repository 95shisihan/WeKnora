"""Host HTTP SDK for grpc.Server or the companion StdioServer adapter."""
import base64
import json
import queue
import threading
import time

import grpc
from google.protobuf.wrappers_pb2 import BytesValue

MAX_WIRE = 4 << 20


class HostHTTPError(Exception):
    def __init__(self, code, message):
        self.code = code
        super().__init__(f"{code}: {message}")


class HostHTTP:
    def __init__(self, server):
        self._lock = threading.Condition()
        self._session = None
        self._next = 0
        handler = grpc.stream_stream_rpc_method_handler(
            self._open, request_deserializer=BytesValue.FromString,
            response_serializer=BytesValue.SerializeToString)
        server.add_generic_rpc_handlers((grpc.method_handlers_generic_handler(
            "weknora.hosthttp.v1.Channel", {"Open": handler}),))

    @staticmethod
    def _encode(frame):
        raw = json.dumps(frame, separators=(",", ":")).encode()
        if len(raw) > MAX_WIRE:
            raise HostHTTPError("REQUEST_TOO_LARGE", "frame exceeds 4 MiB")
        return BytesValue(value=raw)

    def _open(self, responses, context):
        session = {"out": queue.Queue(maxsize=16), "pending": {}, "closed": False}
        with self._lock:
            if self._session is not None:
                context.abort(grpc.StatusCode.ALREADY_EXISTS, "HTTP channel already open")
            self._session = session
            self._lock.notify_all()

        def close():
            with self._lock:
                if session["closed"]:
                    return
                session["closed"] = True
                if self._session is session:
                    self._session = None
                for result in session["pending"].values():
                    result.put_nowait({"error": {"code": "CHANNEL_CLOSED", "message": "host disconnected"}})
                session["pending"].clear()
                self._lock.notify_all()

        def receive():
            try:
                for value in responses:
                    if len(value.value) > MAX_WIRE:
                        return
                    frame = json.loads(value.value)
                    if not frame.get("id") or "response" not in frame:
                        return
                    with self._lock:
                        result = session["pending"].pop(frame["id"], None)
                        if result is not None:
                            result.put_nowait(frame["response"])
            except (grpc.RpcError, ValueError, TypeError, KeyError):
                pass
            finally:
                close()

        context.add_callback(close)
        worker = threading.Thread(target=receive, daemon=True)
        worker.start()
        try:
            yield self._encode({"ready": True})
            while context.is_active() and not session["closed"]:
                try:
                    yield session["out"].get(timeout=0.1)
                except queue.Empty:
                    continue
        finally:
            close()

    def request(self, method, url, *, headers=None, body=b"", timeout=20, active=lambda: True):
        """Returns statusCode/headers/body; body is bytes. Never retries writes."""
        if not 0 < timeout <= 30:
            raise ValueError("timeout must be between 0 and 30 seconds")
        if len(body) > 2 << 20:
            raise HostHTTPError("REQUEST_TOO_LARGE", "request body exceeds 2 MiB")
        deadline = time.monotonic() + timeout
        payload = {"method": method, "url": url, "headers": headers or {},
                   "body": base64.b64encode(body).decode(), "timeoutSeconds": max(1, int(timeout))}
        with self._lock:
            while self._session is None:
                if not active():
                    raise HostHTTPError("CANCELLED", "caller cancelled")
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise HostHTTPError("TIMEOUT", "host channel unavailable")
                self._lock.wait(min(remaining, .1))
            session = self._session
            if len(session["pending"]) >= 4:
                raise HostHTTPError("BUSY", "four HTTP requests are already active")
            self._next += 1
            request_id = self._next
            frame = self._encode({"id": request_id, "request": payload})
            result = queue.Queue(maxsize=1)
            try:
                session["out"].put_nowait(frame)
            except queue.Full:
                raise HostHTTPError("BUSY", "HTTP channel queue is full") from None
            session["pending"][request_id] = result
        try:
            while True:
                if not active():
                    try:
                        session["out"].put_nowait(self._encode({"id": request_id, "cancel": True}))
                    except queue.Full:
                        pass
                    raise HostHTTPError("CANCELLED", "caller cancelled")
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise queue.Empty()
                try:
                    response = result.get(timeout=min(.1, remaining))
                    break
                except queue.Empty:
                    continue
            if response.get("error"):
                error = response["error"]
                raise HostHTTPError(error["code"], error["message"])
            response["body"] = base64.b64decode(response.get("body", ""), validate=True)
            return response
        except queue.Empty:
            try:
                session["out"].put_nowait(self._encode({"id": request_id, "cancel": True}))
            except queue.Full:
                pass  # host still enforces its own deadline
            raise HostHTTPError("TIMEOUT", "HTTP request deadline exceeded") from None
        finally:
            with self._lock:
                session["pending"].pop(request_id, None)
