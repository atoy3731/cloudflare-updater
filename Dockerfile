FROM python:3

ENV PYTHONUNBUFFERED=1

COPY requirements.txt /tmp/requirements.txt

RUN pip install -r /tmp/requirements.txt && \
    rm -f /tmp/requirements.txt

COPY application /application

WORKDIR /application

CMD ["python", "app.py"]