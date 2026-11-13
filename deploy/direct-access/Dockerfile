FROM python:3.13-slim
WORKDIR /app
COPY requirements.lock ./
RUN pip install --no-cache-dir --only-binary=:all: -r requirements.lock && useradd --system --uid 10001 sub6-direct
COPY app.py policy.py ./
COPY static ./static
USER sub6-direct
EXPOSE 1998
CMD ["gunicorn", "--bind", "0.0.0.0:1998", "--workers", "2", "--threads", "4", "--access-logfile", "/dev/null", "app:app"]
