async function handleFormSubmit(e) {
  e.preventDefault();

  // Send the query and image to the server
  const response = await fetch("/execute-query", {
    method: "POST",
    body: new FormData(e.target),
  });
  const data = await response.json();

  // Redirect the user's browser to the "/select-text" endpoint with the returned text as a query parameter
  window.location.href = `/select-text?text=${data.text}`;
}
