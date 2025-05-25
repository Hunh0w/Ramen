FROM python:3.12-alpine

RUN apk add ffmpeg flac

ADD requirements.txt requirements.txt
ADD main.py main.py
COPY audio_responses/ audio_responses/

RUN python3 -m pip install -r requirements.txt


EXPOSE 8000
ENTRYPOINT ["python3", "-m", "fastapi", "run", "main.py", "--proxy-headers", "--port", "8000"]