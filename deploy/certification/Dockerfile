FROM python:3.12.11-slim@sha256:27f90d79cc85e9b7b2560063ef44fa0e9eaae7a7c3f5a9f74563065c5477cc24

RUN pip install --no-cache-dir httpx==0.28.1
COPY test/soak/bounded_inference_scale.py /opt/fleet-certification/bounded_inference_scale.py
COPY test/soak/cluster_resource_snapshot.py /opt/fleet-certification/cluster_resource_snapshot.py
COPY test/soak/merge_certification_evidence.py /opt/fleet-certification/merge_certification_evidence.py

USER 65532:65532
ENTRYPOINT ["python3"]
