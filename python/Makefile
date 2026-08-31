.PHONY: install test lint

install:
	pip install -e ".[dev]"

test:
	python -m pytest tests/ -v

lint:
	python -m py_compile fleet_analytics/__init__.py
	python -m py_compile fleet_analytics/capacity_planner.py
	python -m py_compile fleet_analytics/cost_optimizer.py
	python -m py_compile fleet_analytics/anomaly_detector.py
