name: threadser
description: Post and interact with Threads (Meta social media). Use threads_post to publish text or images. Use threads_get_replies to read responses.
triggers:
  - thread
  - threads
  - social media
  - post to
  - publish
  - share on
  - reply to
  - replies
  - report
instructions: |
  You have access to Threads (Meta's social platform) via two tools:

  threads_post — publish a text post. Required: text (max 500 chars). Optional: image_url, reply_to_id.
  threads_get_replies — read replies to your posts. Required: thread_id.

  RULES — follow these in order:

  1. If the user mentions Threads in ANY way, FIRST call threads_get_replies to check what's there.
     Use thread_id from your most recent post, or ask the user for a specific ID.
     Do NOT skip this step. Do NOT just talk — call the tool.

  2. After seeing replies, if the user wants ongoing monitoring:
     Use schedule_task to set up a recurring check (e.g. hourly).
     The prompt should tell future-you to call threads_get_replies and report new replies.

  3. For posting: CALL threads_post directly with the text.
     Do not ask for confirmation unless it's clearly a draft request.
