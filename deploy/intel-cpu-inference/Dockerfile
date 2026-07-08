FROM registry.access.redhat.com/ubi9/python-311:latest

USER 0
RUN pip install --no-cache-dir optimum-intel openvino openvino-tokenizers transformers torch fastapi uvicorn 2>&1 | tail -5
USER 1001

WORKDIR /app
COPY serve.py /app/serve.py

ENV MODEL_NAME=cpu-model
ENV MODEL_PATH=/mnt/models
ENV OMP_NUM_THREADS=32

EXPOSE 8000

CMD ["uvicorn", "serve:app", "--host", "0.0.0.0", "--port", "8000", "--workers", "2"]
