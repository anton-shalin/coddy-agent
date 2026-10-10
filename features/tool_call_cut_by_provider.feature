Feature: A tool call the output limit cut off is told from a whole one
  When the model's output limit ends an answer in the middle of a tool call,
  the agent must not run what is left of the call. It learns that from the
  provider's own events, not from a guess: the Codex backend closes a
  function_call item the limit cut with the status "incomplete" (the arguments
  written so far may be empty, or happen to parse), and Anthropic closes every
  content block, the one the limit cut included, so there the arguments show the
  cut and a whole call that takes none is an empty string.

  Scenario: A Codex tool call the output limit cut off is reported cut
    Given a Codex answer that starts a call to "write"
    And the call's arguments stream as {"path":"a.go","content":"pack
    And the output limit stops the answer inside the call
    When Coddy streams the answer
    Then the answer stops at "max_tokens"
    And the call to "write" is reported cut off
    And the call keeps the arguments {"path":"a.go","content":"pack

  Scenario: A Codex tool call that finished before the output limit is reported whole
    Given a Codex answer that starts a call to "read"
    And the call's arguments stream as {"path":"a.go"}
    And the call finishes before the output limit stops the answer
    When Coddy streams the answer
    Then the answer stops at "max_tokens"
    And the call to "read" is not reported cut off
    And the call keeps the arguments {"path":"a.go"}

  Scenario: An Anthropic tool call the output limit cut off is closed like every block and shows the cut in its arguments
    Given an Anthropic answer that starts a call to "write"
    And the call's arguments stream as {"path":"a.go","content":"pack
    And the output limit stops the answer inside the call
    When Coddy streams the answer
    Then the answer stops at "max_tokens"
    And the call to "write" is not reported cut off
    And the call keeps the arguments {"path":"a.go","content":"pack
    And the arguments are not a whole JSON document

  Scenario: An Anthropic tool call without arguments that finished before the output limit is reported whole
    Given an Anthropic answer that starts a call to "coddy_todo_plan_read"
    And the call finishes before the output limit stops the answer
    When Coddy streams the answer
    Then the answer stops at "max_tokens"
    And the call to "coddy_todo_plan_read" is not reported cut off
    And the call keeps no arguments
