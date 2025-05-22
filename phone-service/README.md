
## 1. Create a virtualenv

```
python -m venv .virtualenv
```

## 2. Install requirements

```
./.virtualenv/bin/pip install -r requirements.txt
```

### 3. Launch service

```
./.virtualenv/bin/uvicorn main:app
```