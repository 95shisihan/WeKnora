FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1
WORKDIR /plugin

COPY requirements.txt datasource.proto ./
RUN pip install --no-cache-dir -r requirements.txt \
    && python -m grpc_tools.protoc -I. \
       --python_out=. --grpc_python_out=. datasource.proto

COPY server.py ./
ENTRYPOINT ["python", "/plugin/server.py"]
