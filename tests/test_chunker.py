from keystone.embeddings.chunker import chunk_text


def test_empty():
    assert chunk_text("") == []
    assert chunk_text("   \n  ") == []


def test_short_single_chunk():
    chunks = chunk_text("Hello world. This is short.")
    assert chunks == ["Hello world. This is short."]


def test_paragraphs_packed_under_target():
    text = "Para A.\n\nPara B.\n\nPara C."
    chunks = chunk_text(text, target_chars=200)
    assert len(chunks) == 1
    assert "Para A." in chunks[0] and "Para C." in chunks[0]


def test_long_paragraph_split_with_overlap():
    sentences = " ".join(f"Sentence number {i}." for i in range(50))
    chunks = chunk_text(sentences, target_chars=200, overlap_chars=40)
    assert len(chunks) > 1
    # Overlap means consecutive chunks share some tail/head.
    overlap_found = any(
        chunks[i][-30:].strip()[:10] in chunks[i + 1] for i in range(len(chunks) - 1)
    )
    assert overlap_found
    # No chunk should explode beyond ~ target + overlap.
    assert all(len(c) <= 400 for c in chunks)
