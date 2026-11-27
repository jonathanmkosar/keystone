# 0631. A decision answer holds a probability for every option, the choice follows from them, and yes or no is a question with the options yes and no

November 27, 2026 · decided in P14-02

## The question

The spec says each question has an id, instructions, and a type, `choice` or `yes_no`, with its options as id and description, and that the result holds each question's answer with a probability for every option. Ollama's API keys questions by name, has a third type (`score`), answers a yes or no as one number (`noul`, the probability of true), and adds a choice and a confidence. What exactly does the seam carry?

## The decision

- **Questions are a list, in order, each with an id**, and so are a choice's options. The order is the caller's, and the adapter writes Ollama's objects in that order. Ids are 1 to 100 ASCII letters, digits, dots, dashes and underscores, Ollama's rule for a question's name, which every registry name and effort fits. A choice has 2 to 255 options.
- **`yes_no` has the options `yes` and `no`.** A question may list them, to describe them, and may list nothing else. Its answer has a probability for each, like a choice's, so a caller reads every answer the same way. The adapter turns Ollama's `noul` p into yes p and no 1 - p.
- **An answer is one per question, in the request's order**: the question's id, the choice, and a probability from 0 to 1 for every option and no other, summing to one within a hundredth. The choice must be the most probable option; the adapter and the checks work it out from the probabilities (`seam.ChoiceOf`), the earlier on a tie.
- **Ollama's `confidence` and `score` are left out.** Confidence is worked out from the probabilities, and no caller asks for a score.
- **Usage** is the seam's `Usage`, as the endpoint counts it; zero means it did not say.

## Why

- One shape for both types means the router reads `res.Answer("step").Probabilities[step]` and `res.Answer("simple").Probabilities["yes"]` alike.
- A list keeps the order the options mean ("step n of m, smallest first"), which a JSON object written by Go's map would sort away.
- Working the choice out from the probabilities means it can never disagree with them, whatever an endpoint writes.

## What it costs

- The seam refuses a question type Ollama has (`score`) until something asks for it.
- A hundredth of slack lets through an endpoint that rounds badly; less would refuse answers that are only rounded.
