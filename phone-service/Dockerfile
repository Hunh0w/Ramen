FROM python:3.12-alpine

RUN apk add ffmpeg 

ADD requirements.txt requirements.txt
ADD main.py main.py

RUN python3 -m pip install -r requirements.txt


EXPOSE 8000
ENTRYPOINT ["python3", "-m", "uvicorn", "main:app"]