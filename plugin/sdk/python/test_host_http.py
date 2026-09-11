import json
import queue
import unittest
from concurrent.futures import ThreadPoolExecutor

import grpc
from google.protobuf.wrappers_pb2 import BytesValue
from host_http import HostHTTP, HostHTTPError


class HostHTTPTest(unittest.TestCase):
    def test_reverse_protocol(self):
        server = grpc.server(ThreadPoolExecutor(max_workers=4))
        sdk = HostHTTP(server)
        port = server.add_insecure_port("127.0.0.1:0")
        server.start()
        channel = grpc.insecure_channel(f"127.0.0.1:{port}")
        output = queue.Queue()

        def responses():
            while True:
                frame = output.get()
                if frame is None:
                    return
                yield BytesValue(value=json.dumps(frame).encode())

        stream = channel.stream_stream("/weknora.hosthttp.v1.Channel/Open",
            request_serializer=BytesValue.SerializeToString,
            response_deserializer=BytesValue.FromString)(responses())
        pool = ThreadPoolExecutor(max_workers=1)
        try:
            self.assertTrue(json.loads(next(stream).value)["ready"])
            future = pool.submit(sdk.request, "GET", "https://example.com", timeout=2)
            request = json.loads(next(stream).value)
            self.assertEqual(request["request"]["url"], "https://example.com")
            output.put({"id": request["id"], "response": {"statusCode": 200, "body": "b2s="}})
            self.assertEqual(future.result(timeout=2)["body"], b"ok")
            future = pool.submit(sdk.request, "GET", "https://denied.example", timeout=2)
            request = json.loads(next(stream).value)
            output.put({"id": request["id"], "response": {"error": {"code": "DOMAIN_NOT_ALLOWED", "message": "denied"}}})
            with self.assertRaises(HostHTTPError) as failure:
                future.result(timeout=2)
            self.assertEqual(failure.exception.code, "DOMAIN_NOT_ALLOWED")
        finally:
            stream.cancel()
            output.put(None)
            channel.close()
            server.stop(0).wait()
            pool.shutdown()

    def test_no_channel_times_out(self):
        server = grpc.server(ThreadPoolExecutor(max_workers=1))
        sdk = HostHTTP(server)
        with self.assertRaises(HostHTTPError) as failure:
            sdk.request("GET", "https://example.com", timeout=0.02)
        self.assertEqual(failure.exception.code, "TIMEOUT")


if __name__ == "__main__":
    unittest.main()
