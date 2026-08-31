"""Mock inference server — OpenAI-compatible endpoints for lab environments.

Returns realistic-looking chat completions without a real model.
Simulates latency, token counting, and streaming to exercise
fleet-llm-d's proxy, load shedding, and routing end-to-end.

Responds to all model names — fleet-controller routes by model,
this server accepts whatever arrives.
"""

import json
import os
import random
import time
import uuid

from fastapi import FastAPI, Request
from fastapi.responses import StreamingResponse

app = FastAPI(title="fleet-llm-d Mock Inference Server")

LATENCY_MIN_MS = int(os.environ.get("MOCK_LATENCY_MIN_MS", "200"))
LATENCY_MAX_MS = int(os.environ.get("MOCK_LATENCY_MAX_MS", "800"))

RESPONSES = [
    "I'm a mock inference server running on Intel Xeon CPU. In a production deployment, this would be served by OVMS C++ with INT8 quantization.",
    "fleet-llm-d routes this request through the inference proxy. Connection pooling, load shedding, and health checking are all active.",
    "This response demonstrates the OpenAI-compatible API. The fleet controller proxies /v1/chat/completions to the appropriate backend.",
    "CPU inference on Intel Xeon 6 Granite Rapids costs $0.60/hr compared to $32/hr for H100 GPU — a 53x cost reduction.",
    "The fleet-llm-d proxy adds X-Fleet-Routed-To and X-Fleet-Routing-Reason headers to every response.",
]


@app.get("/health")
async def health():
    return {"status": "ok"}


@app.get("/v1/models")
async def list_models():
    models = [
        {"id": name, "object": "model", "owned_by": "fleet-llm-d-lab"}
        for name in [
            "granite-350m", "granite-2b-int8", "granite-4.1-3b",
            "granite-3.2-sovereign", "granite-3.2-8b",
        ]
    ]
    return {"object": "list", "data": models}


@app.post("/v1/chat/completions")
async def chat_completions(request: Request):
    body = await request.json()
    model = body.get("model", "mock-model")
    messages = body.get("messages", [])
    max_tokens = min(body.get("max_tokens", 128), 512)
    stream = body.get("stream", False)

    latency_s = random.uniform(LATENCY_MIN_MS, LATENCY_MAX_MS) / 1000
    time.sleep(latency_s)

    response_text = random.choice(RESPONSES)
    prompt_tokens = sum(len(m.get("content", "").split()) for m in messages) * 2
    completion_tokens = len(response_text.split()) * 2
    req_id = f"chatcmpl-{uuid.uuid4().hex[:12]}"

    if stream:
        return StreamingResponse(
            _stream_response(req_id, model, response_text, prompt_tokens, completion_tokens),
            media_type="text/event-stream",
        )

    return {
        "id": req_id,
        "object": "chat.completion",
        "created": int(time.time()),
        "model": model,
        "choices": [{
            "index": 0,
            "message": {"role": "assistant", "content": response_text},
            "finish_reason": "stop",
        }],
        "usage": {
            "prompt_tokens": prompt_tokens,
            "completion_tokens": completion_tokens,
            "total_tokens": prompt_tokens + completion_tokens,
        },
    }


@app.post("/v1/completions")
async def completions(request: Request):
    body = await request.json()
    model = body.get("model", "mock-model")
    prompt = body.get("prompt", "")

    latency_s = random.uniform(LATENCY_MIN_MS, LATENCY_MAX_MS) / 1000
    time.sleep(latency_s)

    response_text = random.choice(RESPONSES)
    prompt_tokens = len(prompt.split()) * 2
    completion_tokens = len(response_text.split()) * 2

    return {
        "id": f"cmpl-{uuid.uuid4().hex[:12]}",
        "object": "text_completion",
        "created": int(time.time()),
        "model": model,
        "choices": [{"text": response_text, "index": 0, "finish_reason": "stop"}],
        "usage": {
            "prompt_tokens": prompt_tokens,
            "completion_tokens": completion_tokens,
            "total_tokens": prompt_tokens + completion_tokens,
        },
    }


async def _stream_response(req_id, model, text, prompt_tokens, completion_tokens):
    words = text.split()
    for i, word in enumerate(words):
        chunk = {
            "id": req_id,
            "object": "chat.completion.chunk",
            "created": int(time.time()),
            "model": model,
            "choices": [{
                "index": 0,
                "delta": {"content": word + " "},
                "finish_reason": None,
            }],
        }
        yield f"data: {json.dumps(chunk)}\n\n"
        time.sleep(0.03)

    final = {
        "id": req_id,
        "object": "chat.completion.chunk",
        "created": int(time.time()),
        "model": model,
        "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}],
    }
    yield f"data: {json.dumps(final)}\n\n"
    yield "data: [DONE]\n\n"


if __name__ == "__main__":
    import uvicorn
    port = int(os.environ.get("PORT", "8080"))
    uvicorn.run(app, host="0.0.0.0", port=port)
