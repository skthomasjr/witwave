FROM python:3.12-slim-bookworm

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

RUN useradd -m -u 1000 tool
USER tool
WORKDIR /home/tool

COPY --chown=tool:tool tools/kubernetes/requirements.txt /home/tool/requirements.txt
RUN pip install --no-cache-dir --user -r /home/tool/requirements.txt

ENV PATH="/home/tool/.local/bin:$PATH"

COPY --chown=tool:tool tools/kubernetes/ /home/tool/

CMD ["python", "/home/tool/server.py"]
