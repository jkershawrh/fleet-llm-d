Feature: ModelPack GPU Requirement Resolution
  As a fleet operator
  I want fleet-llm-d to automatically resolve GPU requirements from ModelPack metadata
  So that placement decisions account for model hardware needs

  Scenario: Resolve GPU requirements for a 70B fp16 model
    Given a model with parameter size "70b" and precision "fp16"
    When I resolve GPU requirements via ModelPack
    Then the estimated GPU memory should be approximately 140 GB

  Scenario: Resolve GPU requirements for a 7B fp16 model
    Given a model with parameter size "7b" and precision "fp16"
    When I resolve GPU requirements via ModelPack
    Then the estimated GPU memory should be approximately 14 GB

  Scenario: Int8 quantization halves memory requirement
    Given a model with parameter size "70b" and precision "int8"
    When I resolve GPU requirements via ModelPack
    Then the estimated GPU memory should be approximately 70 GB

  Scenario: Small model with int4 quantization
    Given a model with parameter size "2b" and precision "int4"
    When I resolve GPU requirements via ModelPack
    Then the estimated GPU memory should be approximately 1 GB
