Feature: Anthropic adaptive thinking preserves effort and tool continuity
  Opus 5.5 uses adaptive thinking without a token budget.
  Older models keep their extended-thinking token budgets.

  Scenario Outline: Continue a signed thinking tool call
    Given Anthropic model "<model>" with reasoning effort "<effort>"
    When Coddy streams a tool call and continues with its result
    Then both requests use "<thinking>" thinking and output effort "<output>"
    And signed thinking is replayed unchanged before tool use

    Examples:
      | model              | effort | thinking | output |
      | claude-opus-5-5    | high   | adaptive | high   |
      | claude-opus-5-5    |        | adaptive | medium |
      | claude-sonnet-4-5  | high   | enabled  |        |
