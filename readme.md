# paypal_hackathon

The NIM evaluator uses `openai/gpt-oss-20b` by default. Set `NIM_MODEL` or pass
another model ID as the first argument to test a different model:

```powershell
$env:NVIDIA_API_KEY = "your-key"
uv run python eval/check_nim.py
uv run python eval/check_nim.py <model-id>
```

To regenerate and validate the evaluation cases:

```powershell
uv run python eval/make_cases.py
uv run python eval/validate_cases.py
```
