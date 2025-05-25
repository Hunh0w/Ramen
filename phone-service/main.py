import csv
import os
import re
from fastapi import FastAPI, Response
from fastapi.responses import FileResponse
import requests
import speech_recognition as sr
from pydub import AudioSegment
from io import BytesIO
from gtts import gTTS
from os import getenv
from datetime import datetime

CSV_FILE = "call_results.csv"
KUBE_AI_BASE_URL = getenv("KUBE_AI_BASE_URL")


app = FastAPI()

@app.post("/tts")
async def tts(text: str):
    msg = ai_request(f"Identify the main problem of this error and explain it in a very short way, I want an extremely short answer: {text}", model = "qwen2.5-coder-1.5b-cpu")

    tts = gTTS(msg, lang='en')
    mp3_fp = BytesIO()
    tts.write_to_fp(mp3_fp)
    mp3_fp.seek(0)

    return Response(content=mp3_fp.read(), media_type="audio/mpeg")

@app.get("/phone_response")
async def phone_response(path: str):
    audio = AudioSegment.from_file(path, format="mp3")
    wav_io = BytesIO()
    audio.export(wav_io, format="wav")
    wav_io.seek(0)

    recognizer = sr.Recognizer()
    with sr.AudioFile(wav_io) as source:
        audio_data = recognizer.record(source)

    text = recognizer.recognize_google(audio_data, language="en-US").lower()
    result = ai_request(f"Answer in one word: Is this text positive, negative or neutral ?\n{text}", model = "qwen2.5-coder-1.5b-cpu")

    return {"text": text, "sentiment": get_sentiment(result.lower())}

@app.get("/phone_call")
async def phone_call(text: str):
    msg = ai_request(f"Identify the main problem of this error and explain it in a very short way, I want an extremely short answer: {text}", model = "qwen2.5-coder-1.5b-cpu")

    # Génération du speech
    tts = gTTS(msg, lang='en')
    mp3_fp = BytesIO()
    tts.write_to_fp(mp3_fp)
    mp3_fp.seek(0)

    # *Appel en cours de l'employé en utilisant le speech généré...*

    # Lecture de la réponse de l'employé
    audio = AudioSegment.from_file("audio_responses/employee_response.mp3", format="mp3")
    wav_io = BytesIO()
    audio.export(wav_io, format="wav")
    wav_io.seek(0)

    recognizer = sr.Recognizer()
    with sr.AudioFile(wav_io) as source:
        audio_data = recognizer.record(source)

    text = recognizer.recognize_google(audio_data, language="en-US").lower()
    result = ai_request(f"Answer in one word: Is this text positive, negative or neutral ?\n{text}", model = "qwen2.5-coder-1.5b-cpu")
    register_result(datetime.now(), get_sentiment(result.lower()), text, msg)

    if not os.path.exists(CSV_FILE):
        return {"error": "Fichier CSV introuvable"}
    
    return FileResponse(
        path=CSV_FILE,
        media_type="text/csv",
        filename="call_results.csv"
    )
    
@app.get("/csv-results")
async def get_csv_results():
    if not os.path.exists(CSV_FILE):
        return {"error": "Fichier CSV introuvable"}
    
    return FileResponse(
        path=CSV_FILE,
        media_type="text/csv",
        filename="call_results.csv"
    )

def get_sentiment(result: str):
    rs = result.lower().replace("é", "e").replace("è", "e")
    if "neu" in rs or "ind" in rs or rs.startswith("oui") or rs.startswith("yes"):
        return False #"neutral"
    if "neg" in rs or "deni" in rs or "deny" in rs or rs.startswith("non") or rs.startswith("no"):
        return False #"negative"
    if "positi" in rs:
        return True #"positive"
    return False #"neutral"

def register_result(date: str, sentiment: bool, text: str, msg: str):
    with open(CSV_FILE, mode='a', newline='', encoding='utf-8') as fichier:
        writer = csv.writer(fichier, delimiter=";")
        writer.writerow([date, sentiment, text, msg])

def ai_request(text: str, model: str = "deepseek-r1-1.5b-cpu"):
    url = KUBE_AI_BASE_URL + "/openai/v1/chat/completions"
    payload = {
        "model": model,
		"messages": [
			{
				"role": "user",
				"content": text,
			},
        ]
    }

    response = requests.post(url, json=payload, headers={"Content-Type": "application/json"})
    message: str = response.json()["choices"][0]["message"]["content"]
    message = re.sub(r'</?think>', '', message)
    message = message.replace("\n", "")
    return message