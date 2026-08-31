"""OpenVINO CPU inference server with OpenAI-compatible API.

Serves any OpenVINO-exported model from /mnt/models/ with:
- /v1/chat/completions (streaming + non-streaming)
- /v1/models
- /health

Designed for multi-worker uvicorn deployment on Intel CPU.
"""

import json
import os
import threading
import time
import uuid

from fastapi import FastAPI
from pydantic import BaseModel
from typing import List, Optional
from starlette.responses import StreamingResponse
from optimum.intel import OVModelForCausalLM
from transformers import AutoTokenizer

MODEL_NAME = os.environ.get("MODEL_NAME", "cpu-model")
MODEL_PATH = os.environ.get("MODEL_PATH", "/mnt/models")
MAX_TOKENS_CAP = int(os.environ.get("MAX_TOKENS_CAP", "1024"))

app = FastAPI()
print(f"Loading OpenVINO model from {MODEL_PATH}")
model = OVModelForCausalLM.from_pretrained(MODEL_PATH, device="CPU")
tokenizer = AutoTokenizer.from_pretrained(MODEL_PATH)
model_lock = threading.Lock()
has_chat_template = hasattr(tokenizer, "chat_template") and tokenizer.chat_template is not None
print(f"Model {MODEL_NAME} loaded, chat_template={has_chat_template}")


class Message(BaseModel):
    role: str
    content: str


class ChatRequest(BaseModel):
    model: str = MODEL_NAME
    messages: List[Message]
    max_tokens: int = 128
    temperature: float = 0.7
    stream: bool = False


@app.get("/v1/models")
def list_models():
    return {"object": "list", "data": [{"id": MODEL_NAME, "object": "model"}]}


@app.get("/health")
def health():
    return {"status": "ok"}


@app.post("/v1/chat/completions")
def chat(req: ChatRequest):
    msgs = [{"role": m.role, "content": m.content} for m in req.messages]
    if has_chat_template:
        prompt = tokenizer.apply_chat_template(msgs, tokenize=False, add_generation_prompt=True)
    else:
        prompt = ""
        for m in req.messages:
            if m.role == "system":
                prompt += f"System: {m.content}\n"
            elif m.role == "user":
                prompt += f"User: {m.content}\nAssistant: "
            else:
                prompt += f"{m.content}\n"
        if not prompt:
            prompt = "Hello"

    inputs = tokenizer(prompt, return_tensors="pt", truncation=True, max_length=2048)
    with model_lock:
        outputs = model.generate(
            **inputs, max_new_tokens=min(req.max_tokens, MAX_TOKENS_CAP), do_sample=False
        )

    text = tokenizer.decode(outputs[0][inputs["input_ids"].shape[1]:], skip_special_tokens=True)
    prompt_tokens = inputs["input_ids"].shape[1]
    comp_tokens = outputs.shape[1] - prompt_tokens
    resp_id = f"chatcmpl-{uuid.uuid4().hex[:12]}"

    if req.stream:
        def generate():
            d = {
                "id": resp_id, "object": "chat.completion.chunk",
                "model": MODEL_NAME,
                "choices": [{"index": 0, "delta": {"role": "assistant"}, "finish_reason": None}],
            }
            yield f"data: {json.dumps(d)}\n\n"
            words = text.split(" ")
            for i, word in enumerate(words):
                chunk = word if i == 0 else " " + word
                d = {
                    "id": resp_id, "object": "chat.completion.chunk",
                    "model": MODEL_NAME,
                    "choices": [{"index": 0, "delta": {"content": chunk}, "finish_reason": None}],
                }
                yield f"data: {json.dumps(d)}\n\n"
            d = {
                "id": resp_id, "object": "chat.completion.chunk",
                "model": MODEL_NAME,
                "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}],
            }
            yield f"data: {json.dumps(d)}\n\n"
            yield "data: [DONE]\n\n"

        return StreamingResponse(generate(), media_type="text/event-stream")

    return {
        "id": resp_id,
        "object": "chat.completion",
        "model": MODEL_NAME,
        "choices": [{"index": 0, "message": {"role": "assistant", "content": text}, "finish_reason": "stop"}],
        "usage": {"prompt_tokens": prompt_tokens, "completion_tokens": comp_tokens, "total_tokens": prompt_tokens + comp_tokens},
    }
