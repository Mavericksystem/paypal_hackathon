import os
import sys
import time

import httpx
from dotenv import load_dotenv

load_dotenv()
for model in sys.argv[1:]:
    t = time.time()
    try:
        r = httpx.post(
            "https://integrate.api.nvidia.com/v1/chat/completions",
            headers={"Authorization": "Bearer " + os.environ["NVIDIA_API_KEY"]},
            json={
                "model": model,
                "messages": [{"role": "user", "content": "Reply with the word ok."}],
                "max_tokens": 20,
            },
            timeout=20,
        )
        print(f"{model}: HTTP {r.status_code} in {time.time() - t:.1f}s")
    except httpx.HTTPError as e:
        print(f"{model}: {type(e).__name__} after {time.time() - t:.1f}s")
