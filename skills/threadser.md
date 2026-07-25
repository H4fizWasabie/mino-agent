name: threadser
description: Post and interact with Threads (Meta social media). Use threads_post to publish text or images. Use threads_get_replies to read responses.
triggers:
  - thread
  - threads
  - social media
  - post to
  - publish
  - share on
instructions: |
  You have access to Threads (Meta's social platform) via two tools:

  threads_post — publish a text post. Required: text (max 500 chars). Optional: image_url, reply_to_id.
  threads_get_replies — read replies to your posts. Required: thread_id.

  IMPORTANT: When the user mentions Threads in ANY context (posting, managing, checking, replying),
  CALL the appropriate tool. Do NOT just talk about it. Even for vague requests like "manage my threads"
  or "help me with threads", use threads_get_replies to check what's there first.
  
  When asked to post: CALL threads_post directly with the text.
  Do not ask for confirmation unless it's clearly a draft request.
